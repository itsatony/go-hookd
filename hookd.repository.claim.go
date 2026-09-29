// Package hookd — delivery claims (v0.11.0, go-hookd#1).
//
// WHY: until v0.11.0 the worker poll was an autocommit
// `SELECT ... FOR UPDATE SKIP LOCKED`. A row lock lives until the end of its
// TRANSACTION, and in autocommit the transaction ends with the statement, so the
// row was unlocked — and still `pending` — before the worker even started to
// deliver it. SKIP LOCKED only excluded rows whose SELECT happened to be running
// at that instant; two workers whose polls did not overlap both delivered the
// same row. Hidden only because the poll took one row at a time and queues were
// mostly empty.
//
// THE CLAIM is a LEASE carried by the row itself, in the column the poll already
// filters on:
//
//   - ClaimPendingDeliveries: ONE statement selects due rows FOR UPDATE SKIP
//     LOCKED and sets their next_retry_at to now+lease. The update commits with
//     the statement, so the claim outlives it, and no other poll sees the row
//     until the lease expires. No new column, no schema version bump (a bump
//     would DROP every consumer's tables, see SchemaManager.EnsureSchema), and no
//     reaper: a crashed worker's row simply becomes due again.
//   - The claimed next_retry_at is a FENCING TOKEN. What correctness needs is
//     that no two live claims on a row share a token: a re-claim requires
//     now >= the previous value and writes now+lease (so it exceeds every token
//     issued before it), and a renewal writes GREATEST(now+lease, token+1µs).
//     Other writers (the retry schedule, a circuit-open release, a redrive) may
//     set lower, app-clock values — those end the claim, they never forge one.
//   - RenewDeliveryClaim is called immediately before the HTTP send and only
//     succeeds if the row still carries the caller's token. So a row whose lease
//     lapsed while it waited behind others in a batch, and was re-claimed, is
//     SKIPPED by the stale worker instead of being delivered twice.
//   - After the renewal the lease must outlast the send (DeliveryTimeout) plus
//     the bookkeeping writes (DeliveryBookkeepingTimeout); Config.Validate
//     enforces ClaimLeaseMs >= both + MinClaimLeaseMarginMs.
//   - ReleaseDeliveryClaim hands an unsent row back (shutdown, open circuit).
//
// Delivery stays AT-LEAST-ONCE — a worker can send and die before recording the
// result — which is why every request carries X-Webhook-Delivery-ID (and
// X-Webhook-Idempotency-Key when the queuer gave one) for receivers to dedupe.
package hookd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/itsatony/go-cuserr"
)

// claimExecer is what both *sql.DB and *sql.Tx provide.
type claimExecer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Operation names recorded in claim error metadata.
const (
	opClaimPendingDeliveries = "claim_pending_deliveries"
	opScanClaimedDelivery    = "scan_claimed_delivery"
	opRenewDeliveryClaim     = "renew_delivery_claim"
	opReleaseDeliveryClaim   = "release_delivery_claim"
	opRequeueDeadLetter      = "requeue_dead_letter"
	opStoreIdempotencyKey    = "store_idempotency_key"
	opRowsAffected           = "rows_affected"
	metaKeyOperation         = "operation"
	errSourceDatabase        = "database"
	errProviderPostgres      = "postgres"
)

// validateClaimArgs is the argument check every claim implementation shares.
func validateClaimArgs(limit int, lease time.Duration) error {
	if limit < 1 {
		return cuserr.NewValidationError("limit", ErrMsgInvalidClaimLimit)
	}
	if lease <= 0 {
		return cuserr.NewValidationError("lease", ErrMsgInvalidClaimLeaseDuration)
	}
	return nil
}

// claimDB wraps a database failure the way the rest of the repository does.
func claimDB(err error, op string) error {
	return cuserr.NewExternalError(errSourceDatabase, errProviderPostgres, err,
		cuserr.WithMetadata(metaKeyOperation, op),
	)
}

// pgClaimPendingDeliveries is ClaimPendingDeliveries for PostgresRepository and
// PostgresRepositoryTx. The CTE's FOR UPDATE SKIP LOCKED only arbitrates between
// claimers running at the same instant; what keeps the claim afterwards is the
// committed next_retry_at. due_at (the pre-claim queue position) is returned so
// the batch keeps the poll's oldest-due-first order, which RETURNING does not
// guarantee.
func pgClaimPendingDeliveries(ctx context.Context, q claimExecer, table string, limit int, lease time.Duration) ([]*Delivery, error) {
	if err := validateClaimArgs(limit, lease); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
		WITH due AS (
			SELECT id, COALESCE(next_retry_at, created_at) AS due_at
			FROM %[1]s
			WHERE status = $1
			  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
			ORDER BY COALESCE(next_retry_at, created_at), created_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE %[1]s AS d
		SET next_retry_at = NOW() + ($3::bigint * INTERVAL '1 microsecond')
		FROM due
		WHERE d.id = due.id
		RETURNING d.id, d.subscription_id, d.tenant_id, d.event_type, d.payload,
		          d.url, d.secret, d.status, d.attempt_count, d.max_attempts,
		          d.next_retry_at, d.completed_at, d.created_at, d.idempotency_key, due.due_at`, table)

	rows, err := q.QueryContext(ctx, query, DeliveryStatusPending, limit, lease.Microseconds())
	if err != nil {
		return nil, claimDB(err, opClaimPendingDeliveries)
	}
	defer func() { _ = rows.Close() }()

	type claimed struct {
		delivery *Delivery
		dueAt    time.Time
	}
	var batch []claimed
	for rows.Next() {
		var dueAt time.Time
		delivery, scanErr := scanDelivery(scanWithTrailing{rows: rows, trailing: []any{&dueAt}})
		if scanErr != nil {
			return nil, claimDB(scanErr, opScanClaimedDelivery)
		}
		batch = append(batch, claimed{delivery: delivery, dueAt: dueAt})
	}
	if err := rows.Err(); err != nil {
		return nil, claimDB(err, opClaimPendingDeliveries)
	}

	sort.SliceStable(batch, func(i, j int) bool {
		if !batch[i].dueAt.Equal(batch[j].dueAt) {
			return batch[i].dueAt.Before(batch[j].dueAt)
		}
		return batch[i].delivery.CreatedAt.Before(batch[j].delivery.CreatedAt)
	})
	deliveries := make([]*Delivery, 0, len(batch))
	for _, c := range batch {
		deliveries = append(deliveries, c.delivery)
	}
	return deliveries, nil
}

// scanWithTrailing lets scanDelivery's fixed column list be followed by extra
// RETURNING columns (due_at) in one Scan call.
type scanWithTrailing struct {
	rows     *sql.Rows
	trailing []any
}

func (s scanWithTrailing) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.trailing...)...)
}

// pgRenewDeliveryClaim is RenewDeliveryClaim for both Postgres repositories.
func pgRenewDeliveryClaim(ctx context.Context, q claimExecer, table, id string, claimedUntil time.Time, lease time.Duration) (time.Time, error) {
	if lease <= 0 {
		return time.Time{}, cuserr.NewValidationError("lease", ErrMsgInvalidClaimLeaseDuration)
	}
	query := fmt.Sprintf(`
		UPDATE %s
		SET next_retry_at = GREATEST(NOW() + ($4::bigint * INTERVAL '1 microsecond'),
		                             next_retry_at + INTERVAL '1 microsecond')
		WHERE id = $1 AND status = $2 AND next_retry_at = $3
		RETURNING next_retry_at`, table)

	var renewed time.Time
	err := q.QueryRowContext(ctx, query, id, DeliveryStatusPending, claimedUntil, lease.Microseconds()).Scan(&renewed)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrDeliveryClaimLost
	}
	if err != nil {
		return time.Time{}, claimDB(err, opRenewDeliveryClaim)
	}
	return renewed, nil
}

// pgReleaseDeliveryClaim is ReleaseDeliveryClaim for both Postgres repositories.
func pgReleaseDeliveryClaim(ctx context.Context, q claimExecer, table, id string, claimedUntil time.Time, retryAt *time.Time) error {
	query := fmt.Sprintf(`
		UPDATE %s SET next_retry_at = $4
		WHERE id = $1 AND status = $2 AND next_retry_at = $3`, table)

	var next sql.NullTime
	if retryAt != nil {
		next = sql.NullTime{Time: *retryAt, Valid: true}
	}
	result, err := q.ExecContext(ctx, query, id, DeliveryStatusPending, claimedUntil, next)
	if err != nil {
		return claimDB(err, opReleaseDeliveryClaim)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return claimDB(err, opRowsAffected)
	}
	if n == 0 {
		return ErrDeliveryClaimLost
	}
	return nil
}

// pgRequeueDeadLetter is RequeueDeadLetter for both Postgres repositories.
func pgRequeueDeadLetter(ctx context.Context, q claimExecer, table, id string) (*Delivery, error) {
	query := fmt.Sprintf(`
		UPDATE %s
		SET status = $2, max_attempts = attempt_count + max_attempts,
		    next_retry_at = NOW(), completed_at = NULL
		WHERE id = $1 AND status = $3
		RETURNING id, subscription_id, tenant_id, event_type, payload,
		          url, secret, status, attempt_count, max_attempts,
		          next_retry_at, completed_at, created_at, idempotency_key`, table)
	delivery, err := scanDelivery(q.QueryRowContext(ctx, query, id, DeliveryStatusPending, DeliveryStatusDeadLetter))
	if err == nil {
		return delivery, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, claimDB(err, opRequeueDeadLetter)
	}
	var exists bool
	if err := q.QueryRowContext(ctx, fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1)`, table), id).Scan(&exists); err != nil {
		return nil, claimDB(err, opRequeueDeadLetter)
	}
	if !exists {
		return nil, ErrDeliveryNotFound
	}
	return nil, ErrDeliveryNotDeadLetter
}

// RequeueDeadLetter re-queues a dead letter atomically (see Repository).
func (r *PostgresRepository) RequeueDeadLetter(ctx context.Context, id string) (*Delivery, error) {
	return pgRequeueDeadLetter(ctx, r.db, r.schemaConfig.TableDeliveries(), id)
}

// RequeueDeadLetter re-queues a dead letter atomically within the transaction.
func (r *PostgresRepositoryTx) RequeueDeadLetter(ctx context.Context, id string) (*Delivery, error) {
	return pgRequeueDeadLetter(ctx, r.tx, r.schemaConfig.TableDeliveries(), id)
}

// mockRequeue is RequeueDeadLetter over a mock's map; the caller holds the lock.
func mockRequeue(deliveries map[string]*Delivery, id string) (*Delivery, error) {
	d, ok := deliveries[id]
	if !ok {
		return nil, ErrDeliveryNotFound
	}
	if d.Status != DeliveryStatusDeadLetter {
		return nil, ErrDeliveryNotDeadLetter
	}
	now := mockClaimTime()
	d.Status = DeliveryStatusPending
	d.MaxAttempts = d.AttemptCount + d.MaxAttempts
	d.NextRetryAt = &now
	d.CompletedAt = nil
	return copyDelivery(d), nil
}

// RequeueDeadLetter re-queues a dead letter in the mock.
func (r *MockRepository) RequeueDeadLetter(ctx context.Context, id string) (*Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.injectError != nil {
		return nil, r.injectError
	}
	return mockRequeue(r.deliveries, id)
}

// RequeueDeadLetter re-queues a dead letter within the mock transaction.
func (tx *MockRepositoryTx) RequeueDeadLetter(ctx context.Context, id string) (*Delivery, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return mockRequeue(tx.deliveries, id)
}

// ClaimPendingDeliveries claims up to limit due deliveries (see Repository).
func (r *PostgresRepository) ClaimPendingDeliveries(ctx context.Context, limit int, lease time.Duration) ([]*Delivery, error) {
	return pgClaimPendingDeliveries(ctx, r.db, r.schemaConfig.TableDeliveries(), limit, lease)
}

// RenewDeliveryClaim re-arms a held claim (see Repository).
func (r *PostgresRepository) RenewDeliveryClaim(ctx context.Context, id string, claimedUntil time.Time, lease time.Duration) (time.Time, error) {
	return pgRenewDeliveryClaim(ctx, r.db, r.schemaConfig.TableDeliveries(), id, claimedUntil, lease)
}

// ReleaseDeliveryClaim returns a held, unsent delivery to the queue (see Repository).
func (r *PostgresRepository) ReleaseDeliveryClaim(ctx context.Context, id string, claimedUntil time.Time, retryAt *time.Time) error {
	return pgReleaseDeliveryClaim(ctx, r.db, r.schemaConfig.TableDeliveries(), id, claimedUntil, retryAt)
}

// ClaimPendingDeliveries claims within the transaction; the claim becomes
// visible to other claimers when the transaction commits (until then the rows
// stay locked by it).
func (r *PostgresRepositoryTx) ClaimPendingDeliveries(ctx context.Context, limit int, lease time.Duration) ([]*Delivery, error) {
	return pgClaimPendingDeliveries(ctx, r.tx, r.schemaConfig.TableDeliveries(), limit, lease)
}

// RenewDeliveryClaim re-arms a held claim within the transaction.
func (r *PostgresRepositoryTx) RenewDeliveryClaim(ctx context.Context, id string, claimedUntil time.Time, lease time.Duration) (time.Time, error) {
	return pgRenewDeliveryClaim(ctx, r.tx, r.schemaConfig.TableDeliveries(), id, claimedUntil, lease)
}

// ReleaseDeliveryClaim returns a held, unsent delivery within the transaction.
func (r *PostgresRepositoryTx) ReleaseDeliveryClaim(ctx context.Context, id string, claimedUntil time.Time, retryAt *time.Time) error {
	return pgReleaseDeliveryClaim(ctx, r.tx, r.schemaConfig.TableDeliveries(), id, claimedUntil, retryAt)
}

// =============================================================================
// MOCK — models the lease exactly: a claim moves NextRetryAt, nothing else.
// =============================================================================

// mockClaimTime is "now" at the database's precision (microseconds), so a token
// round-trips through the mock exactly as it does through PostgreSQL.
func mockClaimTime() time.Time {
	return time.Now().Truncate(time.Microsecond)
}

// mockClaim is ClaimPendingDeliveries over a mock's maps; the caller holds the lock.
func mockClaim(deliveries map[string]*Delivery, locked map[string]bool, limit int, lease time.Duration) ([]*Delivery, error) {
	if err := validateClaimArgs(limit, lease); err != nil {
		return nil, err
	}
	now := mockClaimTime()
	candidates := []*Delivery{}
	for _, d := range deliveries {
		if d.Status != DeliveryStatusPending || locked[d.ID] {
			continue
		}
		if d.NextRetryAt != nil && d.NextRetryAt.After(now) {
			continue
		}
		candidates = append(candidates, d)
	}
	dueAt := func(d *Delivery) time.Time {
		if d.NextRetryAt != nil {
			return *d.NextRetryAt
		}
		return d.CreatedAt
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := dueAt(candidates[i]), dueAt(candidates[j])
		if a.Equal(b) {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return a.Before(b)
	})

	token := now.Add(lease)
	results := []*Delivery{}
	for i := 0; i < len(candidates) && i < limit; i++ {
		t := token
		candidates[i].NextRetryAt = &t
		results = append(results, copyDelivery(candidates[i]))
	}
	return results, nil
}

// mockHeldClaim returns the stored row if it still carries claimedUntil.
func mockHeldClaim(deliveries map[string]*Delivery, id string, claimedUntil time.Time) (*Delivery, bool) {
	d, ok := deliveries[id]
	if !ok || d.Status != DeliveryStatusPending || d.NextRetryAt == nil || !d.NextRetryAt.Equal(claimedUntil) {
		return nil, false
	}
	return d, true
}

// mockRenew is RenewDeliveryClaim over a mock's map; the caller holds the lock.
func mockRenew(deliveries map[string]*Delivery, id string, claimedUntil time.Time, lease time.Duration) (time.Time, error) {
	if lease <= 0 {
		return time.Time{}, cuserr.NewValidationError("lease", ErrMsgInvalidClaimLeaseDuration)
	}
	d, ok := mockHeldClaim(deliveries, id, claimedUntil)
	if !ok {
		return time.Time{}, ErrDeliveryClaimLost
	}
	renewed := mockClaimTime().Add(lease)
	if floor := claimedUntil.Add(time.Microsecond); renewed.Before(floor) {
		renewed = floor
	}
	d.NextRetryAt = &renewed
	return renewed, nil
}

// mockRelease is ReleaseDeliveryClaim over a mock's map; the caller holds the lock.
func mockRelease(deliveries map[string]*Delivery, id string, claimedUntil time.Time, retryAt *time.Time) error {
	d, ok := mockHeldClaim(deliveries, id, claimedUntil)
	if !ok {
		return ErrDeliveryClaimLost
	}
	if retryAt == nil {
		d.NextRetryAt = nil
		return nil
	}
	next := *retryAt
	d.NextRetryAt = &next
	return nil
}

// ClaimPendingDeliveries claims due deliveries in the mock (honours InjectError).
func (r *MockRepository) ClaimPendingDeliveries(ctx context.Context, limit int, lease time.Duration) ([]*Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.injectError != nil {
		return nil, r.injectError
	}
	return mockClaim(r.deliveries, r.lockedDeliveries, limit, lease)
}

// RenewDeliveryClaim re-arms a held claim in the mock.
func (r *MockRepository) RenewDeliveryClaim(ctx context.Context, id string, claimedUntil time.Time, lease time.Duration) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.injectError != nil {
		return time.Time{}, r.injectError
	}
	return mockRenew(r.deliveries, id, claimedUntil, lease)
}

// ReleaseDeliveryClaim returns a held delivery to the queue in the mock.
func (r *MockRepository) ReleaseDeliveryClaim(ctx context.Context, id string, claimedUntil time.Time, retryAt *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.injectError != nil {
		return r.injectError
	}
	return mockRelease(r.deliveries, id, claimedUntil, retryAt)
}

// ClaimPendingDeliveries claims due deliveries within the mock transaction.
func (tx *MockRepositoryTx) ClaimPendingDeliveries(ctx context.Context, limit int, lease time.Duration) ([]*Delivery, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return mockClaim(tx.deliveries, tx.lockedDeliveries, limit, lease)
}

// RenewDeliveryClaim re-arms a held claim within the mock transaction.
func (tx *MockRepositoryTx) RenewDeliveryClaim(ctx context.Context, id string, claimedUntil time.Time, lease time.Duration) (time.Time, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return mockRenew(tx.deliveries, id, claimedUntil, lease)
}

// ReleaseDeliveryClaim returns a held delivery within the mock transaction.
func (tx *MockRepositoryTx) ReleaseDeliveryClaim(ctx context.Context, id string, claimedUntil time.Time, retryAt *time.Time) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return mockRelease(tx.deliveries, id, claimedUntil, retryAt)
}

// pgStoreIdempotencyKey records key within scope, REFUSING a key that is still
// live (a cuserr conflict — what QueueDelivery / QueueInlineDelivery treat as a
// duplicate) and taking over an expired one. Until v0.11.1 this was an
// unconditional upsert, so on PostgreSQL no duplicate was ever detected (only
// the mock refused one).
func pgStoreIdempotencyKey(ctx context.Context, q claimExecer, table, key, scope string, expiresAt time.Time) error {
	query := fmt.Sprintf(`
		INSERT INTO %[1]s AS t (idempotency_key, subscription_id, expires_at, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (idempotency_key, subscription_id) DO UPDATE
		SET expires_at = EXCLUDED.expires_at, created_at = EXCLUDED.created_at
		WHERE t.expires_at <= NOW()`, table)
	res, err := q.ExecContext(ctx, query, key, scope, expiresAt)
	if err != nil {
		return claimDB(err, opStoreIdempotencyKey)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return claimDB(err, opRowsAffected)
	}
	if n == 0 {
		return cuserr.NewConflictError("idempotency_key", key, ErrMsgDuplicateIdempotencyKey)
	}
	return nil
}
