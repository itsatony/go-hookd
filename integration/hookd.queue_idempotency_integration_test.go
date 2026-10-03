//go:build integration

package integration_test

import (
	"context"
	"database/sql"
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

// go-hookd#10: until v0.11.1 the PostgreSQL StoreIdempotencyKey was an
// unconditional upsert, so the conflict QueueDelivery treats as "already
// queued" never happened on PostgreSQL and a repeated key was queued and
// delivered again. Only the mock refused, and every dedupe test ran against
// the mock. These tests pin the contract on the REAL store, at the level a
// consumer sees it (rows queued, requests received), and run the same contract
// against the mock so the two cannot drift apart again.

const (
	idemEventType   = "idem.test"
	idemTenant      = "tenant_idem"
	idemKey         = "outbox-row-42"
	idemConcurrent  = 16
	idemDeadline    = 30 * time.Second
	idemQuietPeriod = 1500 * time.Millisecond
	idemPoll        = 50 * time.Millisecond
)

// idemReceiver counts the requests it receives and the idempotency keys they carry.
type idemReceiver struct {
	server *httptest.Server
	total  atomic.Int64
	mu     sync.Mutex
	keys   map[string]int
}

func newIdemReceiver(t *testing.T) *idemReceiver {
	t.Helper()
	r := &idemReceiver{keys: map[string]int{}}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.keys[req.Header.Get(hookd.HeaderIdempotencyKey)]++
		r.mu.Unlock()
		r.total.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *idemReceiver) keyCount(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keys[key]
}

// idemBackend is one repository the contract runs against.
type idemBackend struct {
	name string
	// newRepo returns a fresh, empty repository and (PostgreSQL only) its DSN.
	newRepo func(t *testing.T) (hookd.Repository, string)
}

func idemBackends() []idemBackend {
	return []idemBackend{
		{name: "mock", newRepo: func(t *testing.T) (hookd.Repository, string) {
			return hookd.NewMockRepository(), ""
		}},
		{name: "postgres", newRepo: func(t *testing.T) (hookd.Repository, string) {
			dsn := setupClaimSchema(t)
			return newRepo(t, dsn), dsn
		}},
	}
}

// startIdemManager starts a Manager over repo with a subscription aimed at the receiver.
func startIdemManager(t *testing.T, repo hookd.Repository, dsn string, rcv *idemReceiver) (*hookd.Manager, *hookd.Subscription) {
	t.Helper()
	if dsn == "" {
		dsn = "mock"
	}
	cfg := hookd.NewConfig(dsn)
	cfg.WorkerCount = 2
	cfg.QueuePollInterval = 20
	cfg.QueueIdleMaxInterval = 100
	m, err := hookd.NewManager(cfg, repo, hookd.WithAllowPrivateDestinations())
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, m.Start(ctx))
	t.Cleanup(func() { _ = m.Stop() })
	sub, err := m.CreateSubscription(ctx, &hookd.CreateSubscriptionRequest{
		TenantID:   idemTenant,
		URL:        rcv.server.URL,
		EventTypes: []string{idemEventType},
		Secret:     "idem_secret",
	})
	require.NoError(t, err)
	return m, sub
}

func queueKeyed(m *hookd.Manager, subID, key string, payload int) (*hookd.Delivery, error) {
	return m.QueueDelivery(context.Background(), &hookd.QueueDeliveryRequest{
		SubscriptionID: subID,
		EventType:      idemEventType,
		Payload:        map[string]any{"n": payload},
		IdempotencyKey: key,
	})
}

// deliveryRows counts the subscription's delivery ROWS, whatever their status —
// a held duplicate that was not removed counts, because it would be sent.
func deliveryRows(t *testing.T, repo hookd.Repository, subID string) int {
	t.Helper()
	id := subID
	ds, err := repo.ListDeliveries(context.Background(), &hookd.DeliveryFilter{
		SubscriptionID: &id, TenantID: idemTenant, Limit: 1000,
	})
	require.NoError(t, err)
	return len(ds)
}

// TestQueueDelivery_RepeatedKeyQueuesOneDelivery: queueing one key twice (and a
// third time) leaves exactly ONE delivery row, refuses the repeats with
// IsIdempotencyError, and the receiver gets exactly ONE request.
func TestQueueDelivery_RepeatedKeyQueuesOneDelivery(t *testing.T) {
	for _, b := range idemBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo, dsn := b.newRepo(t)
			rcv := newIdemReceiver(t)
			m, sub := startIdemManager(t, repo, dsn, rcv)

			first, err := queueKeyed(m, sub.ID, idemKey, 1)
			require.NoError(t, err)
			for i := 2; i <= 3; i++ {
				d, err := queueKeyed(m, sub.ID, idemKey, i)
				require.Error(t, err, "repeat %d of a live key must be refused", i)
				assert.True(t, hookd.IsIdempotencyError(err), "repeat %d: want an idempotency error, got %v", i, err)
				assert.Nil(t, d)
			}
			assert.Equal(t, 1, deliveryRows(t, repo, sub.ID), "the refused repeats' held rows must be removed")

			require.Eventually(t, func() bool { return rcv.total.Load() >= 1 }, idemDeadline, idemPoll)
			time.Sleep(idemQuietPeriod) // an ABSENCE (no second send) cannot be waited on
			assert.Equal(t, int64(1), rcv.total.Load(), "exactly one request")
			assert.Equal(t, 1, rcv.keyCount(idemKey))

			// The status is written after the receiver answers, so wait on it.
			require.Eventually(t, func() bool {
				got, err := repo.GetDelivery(context.Background(), first.ID)
				return err == nil && got.Status == hookd.DeliveryStatusSuccess
			}, idemDeadline, idemPoll, "the one queued delivery succeeds")

			// A different key on the same subscription is NOT a duplicate.
			_, err = queueKeyed(m, sub.ID, idemKey+"-other", 4)
			require.NoError(t, err)
			assert.Equal(t, 2, deliveryRows(t, repo, sub.ID))
		})
	}
}

// TestQueueDelivery_ConcurrentRepeatedKeyQueuesOneDelivery: N queuers released
// together with one key — exactly one wins, every other gets
// IsIdempotencyError, and exactly one row (and one request) remains.
func TestQueueDelivery_ConcurrentRepeatedKeyQueuesOneDelivery(t *testing.T) {
	for _, b := range idemBackends() {
		t.Run(b.name, func(t *testing.T) {
			repo, dsn := b.newRepo(t)
			rcv := newIdemReceiver(t)
			m, sub := startIdemManager(t, repo, dsn, rcv)

			var (
				wins, dups atomic.Int64
				otherMu    sync.Mutex
				others     []error
				wg         sync.WaitGroup
			)
			barrier := make(chan struct{})
			for i := 0; i < idemConcurrent; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-barrier
					_, err := queueKeyed(m, sub.ID, idemKey, i)
					switch {
					case err == nil:
						wins.Add(1)
					case hookd.IsIdempotencyError(err):
						dups.Add(1)
					default:
						otherMu.Lock()
						others = append(others, err)
						otherMu.Unlock()
					}
				}(i)
			}
			close(barrier)
			wg.Wait()

			require.Empty(t, others, "no queuer may fail for any other reason")
			assert.Equal(t, int64(1), wins.Load(), "exactly one queuer wins")
			assert.Equal(t, int64(idemConcurrent-1), dups.Load())
			assert.Equal(t, 1, deliveryRows(t, repo, sub.ID), "exactly one row; every loser's held row is removed")

			require.Eventually(t, func() bool { return rcv.total.Load() >= 1 }, idemDeadline, idemPoll)
			time.Sleep(idemQuietPeriod)
			assert.Equal(t, int64(1), rcv.total.Load(), "exactly one request")
		})
	}
}

// TestQueueDelivery_ExpiredKeyIsReusedOnPostgres: once a key's TTL has passed it
// no longer dedupes — the same key queues (and delivers) again.
func TestQueueDelivery_ExpiredKeyIsReusedOnPostgres(t *testing.T) {
	dsn := setupClaimSchema(t)
	repo := newRepo(t, dsn)
	rcv := newIdemReceiver(t)
	m, sub := startIdemManager(t, repo, dsn, rcv)

	_, err := queueKeyed(m, sub.ID, idemKey, 1)
	require.NoError(t, err)
	_, err = queueKeyed(m, sub.ID, idemKey, 2)
	require.True(t, hookd.IsIdempotencyError(err), "live key refused, got %v", err)

	// The TTL is whole hours (IdempotencyTTLHours), so age the stored key directly.
	db := openDB(t, dsn)
	res, err := db.Exec(fmt.Sprintf(
		`UPDATE %s_hookd_idempotency_store SET expires_at = NOW() - INTERVAL '1 minute'
		 WHERE idempotency_key = $1 AND subscription_id = $2`, claimPrefix), idemKey, sub.ID)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "exactly one stored key for (key, subscription)")

	second, err := queueKeyed(m, sub.ID, idemKey, 3)
	require.NoError(t, err, "an expired key is taken over")
	require.NotNil(t, second)
	assert.Equal(t, 2, deliveryRows(t, repo, sub.ID))
	require.Eventually(t, func() bool { return rcv.total.Load() == 2 }, idemDeadline, idemPoll)

	// …and the takeover made it live again.
	_, err = queueKeyed(m, sub.ID, idemKey, 4)
	assert.True(t, hookd.IsIdempotencyError(err), "the taken-over key is live, got %v", err)
	assert.Equal(t, 2, deliveryRows(t, repo, sub.ID))
	assertKeyExpiresInFuture(t, db, sub.ID)
}

func assertKeyExpiresInFuture(t *testing.T, db *sql.DB, subID string) {
	t.Helper()
	var live bool
	require.NoError(t, db.QueryRow(fmt.Sprintf(
		`SELECT expires_at > NOW() FROM %s_hookd_idempotency_store
		 WHERE idempotency_key = $1 AND subscription_id = $2`, claimPrefix), idemKey, subID).Scan(&live))
	assert.True(t, live, "the takeover wrote the new expiry")
}

// idemStore is the slice of Repository / RepositoryTx the store contract needs.
type idemStore interface {
	StoreIdempotencyKey(ctx context.Context, key, subscriptionID string, expiresAt time.Time) error
	CheckIdempotency(ctx context.Context, key, subscriptionID string) (bool, error)
}

// TestStoreIdempotencyKey_ContractMockAndPostgres runs ONE contract against the
// mock and PostgreSQL, each both directly and inside a transaction: a live key
// is refused with a conflict, another scope is independent, an expired key is
// taken over (and is then live).
func TestStoreIdempotencyKey_ContractMockAndPostgres(t *testing.T) {
	for _, b := range idemBackends() {
		for _, inTx := range []bool{false, true} {
			name := b.name
			if inTx {
				name += "/tx"
			}
			t.Run(name, func(t *testing.T) {
				repo, _ := b.newRepo(t)
				ctx := context.Background()
				var s idemStore = repo
				var tx hookd.RepositoryTx
				if inTx {
					var err error
					tx, err = repo.BeginTx(ctx)
					require.NoError(t, err)
					t.Cleanup(func() { _ = tx.Rollback() })
					s = tx
				}
				future := time.Now().Add(time.Hour)

				require.NoError(t, s.StoreIdempotencyKey(ctx, "k1", "scope_a", future))
				err := s.StoreIdempotencyKey(ctx, "k1", "scope_a", future)
				require.Error(t, err, "a live key must be refused")
				assert.True(t, hookd.IsConflictError(err), "want a conflict, got %v", err)
				require.NoError(t, s.StoreIdempotencyKey(ctx, "k1", "scope_b", future), "another scope is independent")

				require.NoError(t, s.StoreIdempotencyKey(ctx, "k2", "scope_a", time.Now().Add(-time.Minute)))
				live, err := s.CheckIdempotency(ctx, "k2", "scope_a")
				require.NoError(t, err)
				assert.False(t, live, "an expired key is not live")
				require.NoError(t, s.StoreIdempotencyKey(ctx, "k2", "scope_a", future), "an expired key is taken over")
				live, err = s.CheckIdempotency(ctx, "k2", "scope_a")
				require.NoError(t, err)
				assert.True(t, live)
				err = s.StoreIdempotencyKey(ctx, "k2", "scope_a", future)
				assert.True(t, hookd.IsConflictError(err), "the taken-over key is live, got %v", err)

				if tx != nil {
					require.NoError(t, tx.Commit())
				}
			})
		}
	}
}
