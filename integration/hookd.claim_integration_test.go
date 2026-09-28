//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	hookd "github.com/itsatony/go-hookd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin go-hookd#1 against a REAL PostgreSQL: the v0.10.0 poll was an
// autocommit SELECT ... FOR UPDATE SKIP LOCKED whose lock was gone before the
// worker saw the row, so two non-overlapping polls delivered the same row.

const (
	claimPrefix       = "claimtest"
	claimSeedCount    = 240
	claimers          = 16
	claimBatch        = 5
	claimLease        = time.Minute
	e2eManagers       = 4
	e2eWorkers        = 4
	e2eDeliveries     = 150
	e2eDeadline       = 90 * time.Second
	e2eEventType      = "claim.test"
	e2eTenant         = "tenant_claim"
	shortLease        = 300 * time.Millisecond
	shortLeaseExpired = 600 * time.Millisecond
)

// setupClaimSchema creates the prefix's schema and returns the DSN.
func setupClaimSchema(t *testing.T) string {
	t.Helper()
	dsn := startPostgres(t)
	cfg, err := hookd.NewSchemaConfig(claimPrefix)
	require.NoError(t, err)
	sm, err := hookd.NewSchemaManagerFromURL(dsn, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), testOpTimeout)
	defer cancel()
	require.NoError(t, sm.EnsureSchema(ctx))
	return dsn
}

// newRepo opens an independent repository (its OWN pool, like a separate process).
func newRepo(t *testing.T, dsn string) *hookd.PostgresRepository {
	t.Helper()
	repo, err := hookd.NewPostgresRepository(dsn, hookd.WithTablePrefix(claimPrefix))
	require.NoError(t, err)
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// seedInline writes n due, pending inline deliveries aimed at url.
func seedInline(t *testing.T, repo *hookd.PostgresRepository, n int, url, tenant string) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < n; i++ {
		id, err := hookd.GenerateDeliveryID()
		require.NoError(t, err)
		require.NoError(t, repo.CreateDelivery(ctx, &hookd.Delivery{
			ID:          id,
			TenantID:    tenant,
			EventType:   e2eEventType,
			Payload:     map[string]any{"i": i},
			URL:         url,
			Status:      hookd.DeliveryStatusPending,
			MaxAttempts: 3,
			CreatedAt:   base.Add(time.Duration(i) * time.Millisecond),
		}))
		ids = append(ids, id)
	}
	return ids
}

// TestClaim_OldAutocommitLockDidNotHold documents the defect deterministically:
// the v0.10.0 query run twice in a row returns the SAME row, because its lock
// died with its statement. ClaimPendingDeliveries run twice returns two
// DIFFERENT rows.
func TestClaim_OldAutocommitLockDidNotHold(t *testing.T) {
	dsn := setupClaimSchema(t)
	repo := newRepo(t, dsn)
	seedInline(t, repo, 2, "https://example.invalid/hook", e2eTenant)
	db := openDB(t, dsn)

	oldPoll := fmt.Sprintf(`SELECT id FROM %s_hookd_deliveries
		WHERE status = 'pending' AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY COALESCE(next_retry_at, created_at), created_at
		LIMIT 1 FOR UPDATE SKIP LOCKED`, claimPrefix)
	var first, second string
	require.NoError(t, db.QueryRow(oldPoll).Scan(&first))
	require.NoError(t, db.QueryRow(oldPoll).Scan(&second))
	assert.Equal(t, first, second, "v0.10.0 poll: the row is handed out again")

	ctx := context.Background()
	a, err := repo.ClaimPendingDeliveries(ctx, 1, claimLease)
	require.NoError(t, err)
	b, err := repo.ClaimPendingDeliveries(ctx, 1, claimLease)
	require.NoError(t, err)
	require.Len(t, a, 1)
	require.Len(t, b, 1)
	assert.NotEqual(t, a[0].ID, b[0].ID, "a claim must survive its statement")

	none, err := repo.ClaimPendingDeliveries(ctx, 10, claimLease)
	require.NoError(t, err)
	assert.Empty(t, none)
}

// TestClaim_BarrierReleasedClaimersNeverOverlap: many independent claimers,
// released together, drain the queue; every row is claimed exactly once.
func TestClaim_BarrierReleasedClaimersNeverOverlap(t *testing.T) {
	dsn := setupClaimSchema(t)
	seed := newRepo(t, dsn)
	ids := seedInline(t, seed, claimSeedCount, "https://example.invalid/hook", e2eTenant)

	repos := make([]*hookd.PostgresRepository, claimers)
	for i := range repos {
		repos[i] = newRepo(t, dsn)
	}

	var (
		mu      sync.Mutex
		claimed = map[string]int{}
		errs    []error
		wg      sync.WaitGroup
	)
	barrier := make(chan struct{})
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func(repo *hookd.PostgresRepository) {
			defer wg.Done()
			<-barrier
			// Bounded: a regression to a non-holding claim never drains the
			// queue, and must FAIL (too many claims) rather than hang.
			for iter := 0; iter < claimSeedCount; iter++ {
				batch, err := repo.ClaimPendingDeliveries(context.Background(), claimBatch, claimLease)
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					return
				}
				if len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, d := range batch {
					claimed[d.ID]++
				}
				mu.Unlock()
			}
		}(repos[i])
	}
	close(barrier)
	wg.Wait()

	require.Empty(t, errs)
	require.Len(t, claimed, len(ids), "every seeded row claimed")
	total := 0
	for _, n := range claimed {
		total += n
	}
	require.Equal(t, len(ids), total, "no row handed out twice")
	for id, n := range claimed {
		assert.Equal(t, 1, n, "row %s claimed %d times", id, n)
	}
}

// TestClaim_ExpiredLeaseFencesStaleHolder: after a lease lapses another worker
// may take the row, and from then on only the new holder can renew or release.
func TestClaim_ExpiredLeaseFencesStaleHolder(t *testing.T) {
	dsn := setupClaimSchema(t)
	stale, fresh := newRepo(t, dsn), newRepo(t, dsn)
	seedInline(t, stale, 1, "https://example.invalid/hook", e2eTenant)
	ctx := context.Background()

	first, err := stale.ClaimPendingDeliveries(ctx, 1, shortLease)
	require.NoError(t, err)
	require.Len(t, first, 1)
	none, err := fresh.ClaimPendingDeliveries(ctx, 1, claimLease)
	require.NoError(t, err)
	require.Empty(t, none, "held lease must hide the row")

	time.Sleep(shortLeaseExpired)
	second, err := fresh.ClaimPendingDeliveries(ctx, 1, claimLease)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.True(t, second[0].NextRetryAt.After(*first[0].NextRetryAt), "tokens only increase")

	_, err = stale.RenewDeliveryClaim(ctx, first[0].ID, *first[0].NextRetryAt, claimLease)
	assert.ErrorIs(t, err, hookd.ErrDeliveryClaimLost, "stale holder must not send")
	assert.ErrorIs(t, stale.ReleaseDeliveryClaim(ctx, first[0].ID, *first[0].NextRetryAt, nil), hookd.ErrDeliveryClaimLost)

	renewed, err := fresh.RenewDeliveryClaim(ctx, second[0].ID, *second[0].NextRetryAt, claimLease)
	require.NoError(t, err)
	assert.True(t, renewed.After(*second[0].NextRetryAt))

	// Release makes it due immediately.
	require.NoError(t, fresh.ReleaseDeliveryClaim(ctx, second[0].ID, renewed, nil))
	again, err := stale.ClaimPendingDeliveries(ctx, 1, claimLease)
	require.NoError(t, err)
	require.Len(t, again, 1)

	// A completed row cannot be renewed.
	done, err := stale.GetDelivery(ctx, again[0].ID)
	require.NoError(t, err)
	done.Status = hookd.DeliveryStatusSuccess
	require.NoError(t, stale.UpdateDelivery(ctx, done))
	_, err = stale.RenewDeliveryClaim(ctx, again[0].ID, *again[0].NextRetryAt, claimLease)
	assert.ErrorIs(t, err, hookd.ErrDeliveryClaimLost)
}

// TestClaim_TransactionalClaim: the tx twin uses the prefixed table, holds its
// rows locked until commit, and leaves them leased after commit.
func TestClaim_TransactionalClaim(t *testing.T) {
	dsn := setupClaimSchema(t)
	repo, other := newRepo(t, dsn), newRepo(t, dsn)
	seedInline(t, repo, 3, "https://example.invalid/hook", e2eTenant)
	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	inTx, err := tx.ClaimPendingDeliveries(ctx, 2, claimLease)
	require.NoError(t, err)
	require.Len(t, inTx, 2)

	outside, err := other.ClaimPendingDeliveries(ctx, 10, claimLease)
	require.NoError(t, err)
	require.Len(t, outside, 1, "rows locked by the open tx are skipped")
	require.NoError(t, tx.Commit())

	after, err := other.ClaimPendingDeliveries(ctx, 10, claimLease)
	require.NoError(t, err)
	assert.Empty(t, after, "committed claims stay leased")
}

// TestManagers_ConcurrentNeverDoubleDeliver drives the whole worker path: several
// Managers (each with its own pool) and several workers each, batch size > 1,
// started together. Every delivery reaches the receiver exactly once.
func TestManagers_ConcurrentNeverDoubleDeliver(t *testing.T) {
	dsn := setupClaimSchema(t)

	var (
		mu    sync.Mutex
		seen  = map[string]int{}
		total atomic.Int64
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Header.Get(hookd.HeaderDeliveryID)]++
		mu.Unlock()
		total.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	seed := newRepo(t, dsn)
	ids := seedInline(t, seed, e2eDeliveries, server.URL, e2eTenant)

	managers := make([]*hookd.Manager, e2eManagers)
	for i := range managers {
		cfg := hookd.NewConfig(dsn)
		cfg.WorkerCount = e2eWorkers
		cfg.MaxBatchSize = claimBatch
		cfg.QueuePollInterval = 10
		cfg.QueueIdleMaxInterval = 50
		m, err := hookd.NewManager(cfg, newRepo(t, dsn), hookd.WithAllowPrivateDestinations())
		require.NoError(t, err)
		managers[i] = m
	}

	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for _, m := range managers {
		wg.Add(1)
		go func(m *hookd.Manager) {
			defer wg.Done()
			<-barrier
			assert.NoError(t, m.Start(context.Background()))
		}(m)
	}
	close(barrier)
	wg.Wait()
	t.Cleanup(func() {
		for _, m := range managers {
			_ = m.Stop()
		}
	})

	require.Eventually(t, func() bool { return countStatus(t, dsn, hookd.DeliveryStatusSuccess) == len(ids) },
		e2eDeadline, 100*time.Millisecond, "all deliveries succeed")

	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, seen, len(ids))
	for id, n := range seen {
		assert.Equal(t, 1, n, "delivery %s sent %d times", id, n)
	}
	assert.Equal(t, int64(len(ids)), total.Load())
}

func countStatus(t *testing.T, dsn, status string) int {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var n int
	require.NoError(t, db.QueryRow(fmt.Sprintf(
		`SELECT COUNT(*) FROM %s_hookd_deliveries WHERE status = $1`, claimPrefix), status).Scan(&n))
	return n
}

// TestCleanup_TenantScopeOnPostgres pins go-hookd#7 on the real SQL.
func TestCleanup_TenantScopeOnPostgres(t *testing.T) {
	dsn := setupClaimSchema(t)
	repo := newRepo(t, dsn)
	seedInline(t, repo, 2, "https://example.invalid/a", "tenant_a")
	seedInline(t, repo, 3, "https://example.invalid/b", "tenant_b")
	ctx := context.Background()
	cutoff := time.Now().Add(time.Hour)

	_, err := repo.CountDeliveriesByFilter(ctx, &hookd.CleanupFilter{CreatedBefore: &cutoff})
	require.Error(t, err)
	_, err = repo.DeleteDeliveriesByFilter(ctx, &hookd.CleanupFilter{CreatedBefore: &cutoff})
	require.Error(t, err)
	require.Equal(t, 5, countAll(t, dsn), "a refused sweep deletes nothing")

	n, err := repo.CountDeliveriesByFilter(ctx, &hookd.CleanupFilter{CreatedBefore: &cutoff, TenantID: "tenant_b"})
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	n, err = repo.DeleteDeliveriesByFilter(ctx, &hookd.CleanupFilter{CreatedBefore: &cutoff, TenantID: "tenant_b"})
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	require.Equal(t, 2, countAll(t, dsn))

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	_, err = tx.DeleteDeliveriesByFilter(ctx, &hookd.CleanupFilter{CreatedBefore: &cutoff})
	require.Error(t, err)
	n, err = tx.DeleteDeliveriesByFilter(ctx, &hookd.CleanupFilter{CreatedBefore: &cutoff, AllTenants: true})
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	require.NoError(t, tx.Commit())
	require.Equal(t, 0, countAll(t, dsn))
}

func countAll(t *testing.T, dsn string) int {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var n int
	require.NoError(t, db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %s_hookd_deliveries`, claimPrefix)).Scan(&n))
	return n
}

// TestManager_StaleClaimNeverSends drives the WORKER path with a lapsed claim:
// another worker re-claimed the row, so the stale holder must not send.
// (Removing the pre-send RenewDeliveryClaim fence makes this fail.)
func TestManager_StaleClaimNeverSends(t *testing.T) {
	dsn := setupClaimSchema(t)
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	staleRepo, freshRepo := newRepo(t, dsn), newRepo(t, dsn)
	seedInline(t, staleRepo, 1, server.URL, e2eTenant)
	ctx := context.Background()
	stale, err := staleRepo.ClaimPendingDeliveries(ctx, 1, shortLease)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	time.Sleep(shortLeaseExpired)
	fresh, err := freshRepo.ClaimPendingDeliveries(ctx, 1, claimLease)
	require.NoError(t, err)
	require.Len(t, fresh, 1)

	// The worker receives the stale row exactly as a batch member whose lease
	// lapsed while it waited; renewal and everything else hit real PostgreSQL.
	wrapped := &staleClaimRepo{PostgresRepository: staleRepo, stale: stale[0]}
	cfg := hookd.NewConfig(dsn)
	cfg.QueuePollInterval = 10
	cfg.QueueIdleMaxInterval = 50
	m, err := hookd.NewManager(cfg, wrapped, hookd.WithAllowPrivateDestinations())
	require.NoError(t, err)
	require.NoError(t, m.Start(ctx))
	require.Eventually(t, func() bool { return wrapped.handedOut.Load() }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	require.NoError(t, m.Stop())
	assert.Equal(t, int64(0), hits.Load(), "a lapsed claim must never send")

	still, err := freshRepo.GetDelivery(ctx, fresh[0].ID)
	require.NoError(t, err)
	assert.True(t, still.NextRetryAt.Equal(*fresh[0].NextRetryAt), "the new holder's claim is untouched")
}

// staleClaimRepo hands out one stale claim, then claims nothing.
type staleClaimRepo struct {
	*hookd.PostgresRepository
	stale     *hookd.Delivery
	handedOut atomic.Bool
}

func (r *staleClaimRepo) ClaimPendingDeliveries(_ context.Context, _ int, _ time.Duration) ([]*hookd.Delivery, error) {
	if r.handedOut.CompareAndSwap(false, true) {
		return []*hookd.Delivery{r.stale}, nil
	}
	return nil, nil
}

// TestRetryDeadLetter_ConcurrentRedrivesRequeueOnce: barrier-released redrives
// of one dead letter — exactly one succeeds.
func TestRetryDeadLetter_ConcurrentRedrivesRequeueOnce(t *testing.T) {
	dsn := setupClaimSchema(t)
	repo := newRepo(t, dsn)
	ids := seedInline(t, repo, 1, "https://example.invalid/hook", e2eTenant)
	ctx := context.Background()
	require.NoError(t, repo.MoveToDeadLetter(ctx, ids[0], "test"))

	m, err := hookd.NewManager(hookd.NewConfig(dsn), repo)
	require.NoError(t, err)
	var ok, refused atomic.Int32
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < claimers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			if _, err := m.RetryDeadLetter(ctx, ids[0]); err == nil {
				ok.Add(1)
			} else if errors.Is(err, hookd.ErrDeliveryNotDeadLetter) {
				refused.Add(1)
			}
		}()
	}
	close(barrier)
	wg.Wait()
	assert.Equal(t, int32(1), ok.Load())
	assert.Equal(t, int32(claimers-1), refused.Load())
}
