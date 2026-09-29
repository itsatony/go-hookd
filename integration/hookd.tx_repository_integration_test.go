//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	hookd "github.com/itsatony/go-hookd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin go-hookd#8 against a REAL PostgreSQL: before v0.11.4 every
// CRUD method of PostgresRepositoryTx named the pre-v0.6.0 unprefixed tables
// (and an 11-column delivery list), so any call through BeginTx failed. Each
// RepositoryTx method is exercised INSIDE a transaction and its effect is
// checked three ways: visible within the transaction, invisible to another
// session until Commit, and gone after Rollback.

const (
	txPrefix    = "txrepo"
	txTenant    = "tenant_tx"
	txOther     = "tenant_tx_other"
	txEventType = "tx.test"
	txLease     = time.Minute
)

// txFixture is one database with the schema, and an independent pool repository
// used as the "other session" observer and for seeding committed rows.
type txFixture struct {
	repo *hookd.PostgresRepository
}

func newTxFixture(t *testing.T) *txFixture {
	t.Helper()
	dsn := startPostgres(t)
	cfg, err := hookd.NewSchemaConfig(txPrefix)
	require.NoError(t, err)
	sm, err := hookd.NewSchemaManagerFromURL(dsn, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sm.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), testOpTimeout)
	defer cancel()
	require.NoError(t, sm.EnsureSchema(ctx))
	repo, err := hookd.NewPostgresRepository(dsn, hookd.WithTablePrefix(txPrefix))
	require.NoError(t, err)
	t.Cleanup(func() { _ = repo.Close() })
	return &txFixture{repo: repo}
}

// begin opens a transaction that is rolled back at cleanup unless committed.
func (f *txFixture) begin(t *testing.T) hookd.RepositoryTx {
	t.Helper()
	tx, err := f.repo.BeginTx(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func newSub(t *testing.T, tenant, url string) *hookd.Subscription {
	t.Helper()
	id, err := hookd.GenerateSubscriptionID()
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Microsecond)
	return &hookd.Subscription{
		ID:          id,
		TenantID:    tenant,
		URL:         url,
		Secret:      "tx-secret-0123456789",
		EventTypes:  []string{txEventType},
		Filters:     map[string]string{"k": "v"},
		Status:      hookd.SubscriptionStatusActive,
		RetryPolicy: &hookd.RetryPolicy{MaxAttempts: 4, InitialBackoff: time.Second, MaxBackoff: time.Minute, BackoffFactor: 2},
		Headers:     map[string]string{"X-A": "1"},
		Metadata:    map[string]any{"m": "x"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func newDelivery(t *testing.T, subID, tenant string) *hookd.Delivery {
	t.Helper()
	id, err := hookd.GenerateDeliveryID()
	require.NoError(t, err)
	d := &hookd.Delivery{
		ID:             id,
		SubscriptionID: subID,
		TenantID:       tenant,
		EventType:      txEventType,
		Payload:        map[string]any{"n": float64(1)},
		Status:         hookd.DeliveryStatusPending,
		MaxAttempts:    3,
		IdempotencyKey: "idem-" + id,
		CreatedAt:      time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond),
	}
	if subID == "" {
		d.URL = "https://example.invalid/inline"
		d.Secret = "inline-secret-0123456789"
	}
	return d
}

func newAttempt(t *testing.T, deliveryID string, n int) *hookd.DeliveryAttempt {
	t.Helper()
	id, err := hookd.GenerateAttemptID()
	require.NoError(t, err)
	return &hookd.DeliveryAttempt{
		ID:              id,
		DeliveryID:      deliveryID,
		AttemptNumber:   n,
		StatusCode:      500,
		ResponseBody:    "boom",
		ResponseHeaders: map[string]string{"X-R": "r"},
		Error:           "server error",
		DurationMs:      12,
		AttemptedAt:     time.Now().UTC().Truncate(time.Microsecond),
	}
}

func strPtr(s string) *string { return &s }

func TestTxRepository_EveryMethodRunsInsideTheTransaction(t *testing.T) {
	f := newTxFixture(t)
	ctx := context.Background()

	t.Run("CreateSubscription/GetSubscription/GetSubscriptionByTenantAndURL: rollback discards", func(t *testing.T) {
		tx := f.begin(t)
		sub := newSub(t, txTenant, "https://example.invalid/create-rb")
		require.NoError(t, tx.CreateSubscription(ctx, sub))

		got, err := tx.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, sub.URL, got.URL)
		assert.Equal(t, sub.Filters, got.Filters, "filters must round-trip (the pre-fix tx insert dropped the column)")
		assert.Equal(t, sub.RetryPolicy.MaxAttempts, got.RetryPolicy.MaxAttempts)

		byURL, err := tx.GetSubscriptionByTenantAndURL(ctx, txTenant, sub.URL)
		require.NoError(t, err)
		assert.Equal(t, sub.ID, byURL.ID)

		_, err = f.repo.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, hookd.ErrSubscriptionNotFound, "uncommitted row must be invisible to other sessions")

		require.NoError(t, tx.Rollback())
		_, err = f.repo.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, hookd.ErrSubscriptionNotFound)
	})

	t.Run("CreateSubscription: duplicate tenant+url maps to ErrDuplicateSubscription", func(t *testing.T) {
		tx := f.begin(t)
		sub := newSub(t, txTenant, "https://example.invalid/dup")
		require.NoError(t, tx.CreateSubscription(ctx, sub))
		dup := newSub(t, txTenant, sub.URL)
		assert.ErrorIs(t, tx.CreateSubscription(ctx, dup), hookd.ErrDuplicateSubscription)
	})

	t.Run("CreateSubscription: commit persists", func(t *testing.T) {
		tx := f.begin(t)
		sub := newSub(t, txTenant, "https://example.invalid/create-commit")
		require.NoError(t, tx.CreateSubscription(ctx, sub))
		require.NoError(t, tx.Commit())
		got, err := f.repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, sub.ID, got.ID)
	})

	t.Run("UpdateSubscription and ListSubscriptions", func(t *testing.T) {
		sub := newSub(t, txTenant, "https://example.invalid/update")
		require.NoError(t, f.repo.CreateSubscription(ctx, sub))

		tx := f.begin(t)
		sub.Status = hookd.SubscriptionStatusPaused
		sub.Filters = map[string]string{"k": "changed"}
		require.NoError(t, tx.UpdateSubscription(ctx, sub))

		got, err := tx.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, hookd.SubscriptionStatusPaused, got.Status)

		list, err := tx.ListSubscriptions(ctx, &hookd.SubscriptionFilter{TenantID: txTenant, Status: hookd.SubscriptionStatusPaused, EventTypes: []string{txEventType}, Limit: 10})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, sub.ID, list[0].ID)

		outside, err := f.repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, hookd.SubscriptionStatusActive, outside.Status, "uncommitted update is invisible")

		missing := newSub(t, txTenant, "https://example.invalid/none")
		assert.ErrorIs(t, tx.UpdateSubscription(ctx, missing), hookd.ErrSubscriptionNotFound)

		_, err = tx.ListSubscriptions(ctx, &hookd.SubscriptionFilter{})
		assert.Error(t, err, "a tenant-less list is refused inside a transaction too")

		require.NoError(t, tx.Rollback())
		after, err := f.repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, hookd.SubscriptionStatusActive, after.Status)
	})

	t.Run("DeleteSubscription", func(t *testing.T) {
		sub := newSub(t, txTenant, "https://example.invalid/delete")
		require.NoError(t, f.repo.CreateSubscription(ctx, sub))
		tx := f.begin(t)
		require.NoError(t, tx.DeleteSubscription(ctx, sub.ID))
		_, err := tx.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, hookd.ErrSubscriptionNotFound)
		assert.ErrorIs(t, tx.DeleteSubscription(ctx, sub.ID), hookd.ErrSubscriptionNotFound)
		_, err = f.repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err, "uncommitted delete is invisible")
		require.NoError(t, tx.Commit())
		_, err = f.repo.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, hookd.ErrSubscriptionNotFound)
	})

	t.Run("CreateDelivery/GetDelivery keep every column (subscription and inline)", func(t *testing.T) {
		sub := newSub(t, txTenant, "https://example.invalid/deliveries")
		require.NoError(t, f.repo.CreateSubscription(ctx, sub))

		tx := f.begin(t)
		bySub := newDelivery(t, sub.ID, txTenant)
		inline := newDelivery(t, "", txTenant)
		require.NoError(t, tx.CreateDelivery(ctx, bySub))
		require.NoError(t, tx.CreateDelivery(ctx, inline))

		got, err := tx.GetDelivery(ctx, bySub.ID)
		require.NoError(t, err)
		assert.Equal(t, bySub.IdempotencyKey, got.IdempotencyKey)
		assert.Equal(t, bySub.MaxAttempts, got.AttemptBudget, "attempt_budget is written (it was left NULL)")
		assert.Equal(t, sub.ID, got.SubscriptionID)

		gotInline, err := tx.GetDelivery(ctx, inline.ID)
		require.NoError(t, err)
		assert.Equal(t, inline.URL, gotInline.URL, "inline url must be written inside a transaction")
		assert.Equal(t, inline.Secret, gotInline.Secret)
		assert.Empty(t, gotInline.SubscriptionID)

		_, err = f.repo.GetDelivery(ctx, bySub.ID)
		assert.ErrorIs(t, err, hookd.ErrDeliveryNotFound)
		_, err = tx.GetDelivery(ctx, "dlv_missing")
		assert.ErrorIs(t, err, hookd.ErrDeliveryNotFound)

		require.NoError(t, tx.Rollback())
		_, err = f.repo.GetDelivery(ctx, inline.ID)
		assert.ErrorIs(t, err, hookd.ErrDeliveryNotFound)
	})

	t.Run("UpdateDelivery, ListDeliveries, MoveToDeadLetter, RequeueDeadLetter", func(t *testing.T) {
		d := newDelivery(t, "", txTenant)
		require.NoError(t, f.repo.CreateDelivery(ctx, d))
		tx := f.begin(t)

		d.AttemptCount = 2
		require.NoError(t, tx.UpdateDelivery(ctx, d))
		list, err := tx.ListDeliveries(ctx, &hookd.DeliveryFilter{TenantID: txTenant, Status: strPtr(hookd.DeliveryStatusPending), EventType: strPtr(txEventType), Limit: 50})
		require.NoError(t, err)
		ids := map[string]int{}
		for _, x := range list {
			ids[x.ID] = x.AttemptCount
		}
		assert.Equal(t, 2, ids[d.ID])

		require.NoError(t, tx.MoveToDeadLetter(ctx, d.ID, "test"))
		dead, err := tx.GetDelivery(ctx, d.ID)
		require.NoError(t, err)
		assert.Equal(t, hookd.DeliveryStatusDeadLetter, dead.Status)
		assert.ErrorIs(t, tx.MoveToDeadLetter(ctx, "dlv_missing", "x"), hookd.ErrDeliveryNotFound)

		requeued, err := tx.RequeueDeadLetter(ctx, d.ID)
		require.NoError(t, err)
		assert.Equal(t, hookd.DeliveryStatusPending, requeued.Status)
		assert.Equal(t, 2+d.MaxAttempts, requeued.MaxAttempts)

		missing := newDelivery(t, "", txTenant)
		assert.ErrorIs(t, tx.UpdateDelivery(ctx, missing), hookd.ErrDeliveryNotFound)
		_, err = tx.ListDeliveries(ctx, &hookd.DeliveryFilter{})
		assert.Error(t, err, "a tenant-less list is refused inside a transaction too")

		outside, err := f.repo.GetDelivery(ctx, d.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, outside.AttemptCount, "uncommitted update is invisible")
		require.NoError(t, tx.Commit())
		after, err := f.repo.GetDelivery(ctx, d.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, after.AttemptCount)
		assert.Equal(t, hookd.DeliveryStatusPending, after.Status)
	})

	t.Run("CreateDeliveryAttempt, GetDeliveryAttempts, DeleteDelivery", func(t *testing.T) {
		d := newDelivery(t, "", txTenant)
		require.NoError(t, f.repo.CreateDelivery(ctx, d))
		require.NoError(t, f.repo.CreateDeliveryAttempt(ctx, newAttempt(t, d.ID, 1)), "one committed attempt")
		tx := f.begin(t)

		a2 := newAttempt(t, d.ID, 2)
		require.NoError(t, tx.CreateDeliveryAttempt(ctx, a2))
		require.NoError(t, tx.CreateDeliveryAttempt(ctx, newAttempt(t, d.ID, 3)))

		atts, err := tx.GetDeliveryAttempts(ctx, d.ID)
		require.NoError(t, err)
		require.Len(t, atts, 3)
		assert.Equal(t, int64(12), atts[1].DurationMs, "duration_ms must be written inside a transaction")
		assert.Equal(t, a2.ResponseHeaders, atts[1].ResponseHeaders)

		outside, err := f.repo.GetDeliveryAttempts(ctx, d.ID)
		require.NoError(t, err)
		assert.Len(t, outside, 1, "only the committed attempt is visible outside")

		require.NoError(t, tx.DeleteDelivery(ctx, d.ID))
		_, err = tx.GetDelivery(ctx, d.ID)
		assert.ErrorIs(t, err, hookd.ErrDeliveryNotFound)
		inTx, err := tx.GetDeliveryAttempts(ctx, d.ID)
		require.NoError(t, err)
		assert.Empty(t, inTx, "the delivery's attempts go with it, inside the transaction")
		assert.ErrorIs(t, tx.DeleteDelivery(ctx, d.ID), hookd.ErrDeliveryNotFound)
		outside, err = f.repo.GetDeliveryAttempts(ctx, d.ID)
		require.NoError(t, err)
		assert.Len(t, outside, 1, "the uncommitted delete is invisible")

		require.NoError(t, tx.Rollback())
		_, err = f.repo.GetDelivery(ctx, d.ID)
		require.NoError(t, err, "rollback restores the deleted delivery")
		restored, err := f.repo.GetDeliveryAttempts(ctx, d.ID)
		require.NoError(t, err)
		assert.Len(t, restored, 1, "and its committed attempt")

		// The pool DeleteDelivery (one statement since v0.11.4) removes both.
		require.NoError(t, f.repo.DeleteDelivery(ctx, d.ID))
		_, err = f.repo.GetDelivery(ctx, d.ID)
		assert.ErrorIs(t, err, hookd.ErrDeliveryNotFound)
		gone, err := f.repo.GetDeliveryAttempts(ctx, d.ID)
		require.NoError(t, err)
		assert.Empty(t, gone)
		assert.ErrorIs(t, f.repo.DeleteDelivery(ctx, d.ID), hookd.ErrDeliveryNotFound)
	})

	t.Run("CreateDeliveryAttempt: duplicate attempt number is a conflict and poisons nothing after rollback", func(t *testing.T) {
		d := newDelivery(t, "", txTenant)
		require.NoError(t, f.repo.CreateDelivery(ctx, d))
		tx := f.begin(t)
		require.NoError(t, tx.CreateDeliveryAttempt(ctx, newAttempt(t, d.ID, 1)))
		err := tx.CreateDeliveryAttempt(ctx, newAttempt(t, d.ID, 1))
		require.Error(t, err)
		require.NoError(t, tx.Rollback())
	})

	t.Run("CheckIdempotency, StoreIdempotencyKey, CountExpired, CleanupExpired", func(t *testing.T) {
		tx := f.begin(t)
		scope := "sub_scope_tx"
		require.NoError(t, tx.StoreIdempotencyKey(ctx, "live", scope, time.Now().Add(time.Hour)))
		require.NoError(t, tx.StoreIdempotencyKey(ctx, "expired", scope, time.Now().Add(-time.Hour)))

		live, err := tx.CheckIdempotency(ctx, "live", scope)
		require.NoError(t, err)
		assert.True(t, live)
		exp, err := tx.CheckIdempotency(ctx, "expired", scope)
		require.NoError(t, err)
		assert.False(t, exp)

		outside, err := f.repo.CheckIdempotency(ctx, "live", scope)
		require.NoError(t, err)
		assert.False(t, outside, "uncommitted key is invisible")

		n, err := tx.CountExpiredIdempotencyKeys(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, int64(1))
		removed, err := tx.CleanupExpiredIdempotencyKeys(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, removed, int64(1))
		n, err = tx.CountExpiredIdempotencyKeys(ctx)
		require.NoError(t, err)
		assert.Zero(t, n)
		require.NoError(t, tx.Commit())

		committed, err := f.repo.CheckIdempotency(ctx, "live", scope)
		require.NoError(t, err)
		assert.True(t, committed)
	})

	t.Run("StoreIdempotencyKey: a live key is refused inside the transaction", func(t *testing.T) {
		tx := f.begin(t)
		require.NoError(t, tx.StoreIdempotencyKey(ctx, "k", "scope_dup", time.Now().Add(time.Hour)))
		err := tx.StoreIdempotencyKey(ctx, "k", "scope_dup", time.Now().Add(time.Hour))
		require.Error(t, err)
	})

	t.Run("GetCircuitBreakerState, UpdateCircuitBreakerState", func(t *testing.T) {
		endpoint := "https://example.invalid/cb"
		tx := f.begin(t)
		st, err := tx.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, hookd.CircuitBreakerStateClosed, st.State, "unknown endpoint is closed")

		now := time.Now().UTC().Truncate(time.Microsecond)
		require.NoError(t, tx.UpdateCircuitBreakerState(ctx, &hookd.CircuitBreakerState{
			Endpoint: endpoint, State: hookd.CircuitBreakerStateOpen, FailureCount: 5,
			LastFailure: now, OpenedAt: now, NextRetryAt: now.Add(time.Minute),
		}))
		st, err = tx.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, hookd.CircuitBreakerStateOpen, st.State)
		assert.Equal(t, 5, st.FailureCount)

		outside, err := f.repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, hookd.CircuitBreakerStateClosed, outside.State)
		require.NoError(t, tx.Rollback())
		after, err := f.repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, hookd.CircuitBreakerStateClosed, after.State)
	})

	t.Run("ClaimPendingDeliveries, RenewDeliveryClaim, ReleaseDeliveryClaim", func(t *testing.T) {
		d := newDelivery(t, "", txOther)
		require.NoError(t, f.repo.CreateDelivery(ctx, d))
		tx := f.begin(t)
		claimed, err := tx.ClaimPendingDeliveries(ctx, 100, txLease)
		require.NoError(t, err)
		var mine *hookd.Delivery
		for _, c := range claimed {
			if c.ID == d.ID {
				mine = c
			}
		}
		require.NotNil(t, mine, "the seeded due delivery is claimed")
		require.NotNil(t, mine.NextRetryAt)

		// The claim is the transaction's: another session skips the locked row
		// (and, before Commit, cannot see the lease either).
		others, err := f.repo.ClaimPendingDeliveries(ctx, 100, txLease)
		require.NoError(t, err)
		for _, o := range others {
			assert.NotEqual(t, d.ID, o.ID, "a row claimed inside an open transaction is not handed to another session")
		}

		renewed, err := tx.RenewDeliveryClaim(ctx, d.ID, *mine.NextRetryAt, txLease)
		require.NoError(t, err)
		assert.True(t, renewed.After(*mine.NextRetryAt) || renewed.Equal(*mine.NextRetryAt))
		require.NoError(t, tx.ReleaseDeliveryClaim(ctx, d.ID, renewed, nil))
		require.NoError(t, tx.Commit())

		// Released and committed: it is due again, and the pool claims it.
		again, err := f.repo.ClaimPendingDeliveries(ctx, 100, txLease)
		require.NoError(t, err)
		found := false
		for _, a := range again {
			found = found || a.ID == d.ID
		}
		assert.True(t, found, "the released claim is back in the queue after Commit")
	})

	t.Run("CountDeliveriesByFilter, DeleteDeliveriesByFilter, GetMaintenanceStats", func(t *testing.T) {
		tenant := "tenant_tx_cleanup"
		for i := 0; i < 3; i++ {
			require.NoError(t, f.repo.CreateDelivery(ctx, newDelivery(t, "", tenant)))
		}
		tx := f.begin(t)
		filter := &hookd.CleanupFilter{TenantID: tenant, EventType: strPtr(txEventType)}
		n, err := tx.CountDeliveriesByFilter(ctx, filter)
		require.NoError(t, err)
		assert.Equal(t, int64(3), n)

		stats, err := tx.GetMaintenanceStats(ctx)
		require.NoError(t, err)
		before := stats.TotalDeliveries

		deleted, err := tx.DeleteDeliveriesByFilter(ctx, filter)
		require.NoError(t, err)
		assert.Equal(t, int64(3), deleted)

		stats, err = tx.GetMaintenanceStats(ctx)
		require.NoError(t, err)
		assert.Equal(t, before-3, stats.TotalDeliveries, "stats read the transaction's own writes")

		outside, err := f.repo.CountDeliveriesByFilter(ctx, filter)
		require.NoError(t, err)
		assert.Equal(t, int64(3), outside, "uncommitted delete is invisible")
		require.NoError(t, tx.Rollback())
		after, err := f.repo.CountDeliveriesByFilter(ctx, filter)
		require.NoError(t, err)
		assert.Equal(t, int64(3), after)
	})

	t.Run("Ping, Close, nested BeginTx, Commit/Rollback idempotence", func(t *testing.T) {
		tx := f.begin(t)
		require.NoError(t, tx.Ping(ctx))
		require.NoError(t, tx.Close())
		_, err := tx.BeginTx(ctx)
		require.Error(t, err, "nested transactions are refused")
		require.NoError(t, tx.Commit())
		require.NoError(t, tx.Commit(), "a second Commit is a no-op")
		require.NoError(t, tx.Rollback(), "Rollback after Commit is a no-op")
	})

	t.Run("one transaction spans several methods atomically", func(t *testing.T) {
		tx := f.begin(t)
		sub := newSub(t, txTenant, "https://example.invalid/atomic")
		require.NoError(t, tx.CreateSubscription(ctx, sub))
		d := newDelivery(t, sub.ID, txTenant)
		require.NoError(t, tx.CreateDelivery(ctx, d), "a delivery may reference a subscription created in the same transaction")
		require.NoError(t, tx.CreateDeliveryAttempt(ctx, newAttempt(t, d.ID, 1)))
		require.NoError(t, tx.StoreIdempotencyKey(ctx, d.IdempotencyKey, sub.ID, time.Now().Add(time.Hour)))
		require.NoError(t, tx.Rollback())

		_, err := f.repo.GetSubscription(ctx, sub.ID)
		assert.True(t, errors.Is(err, hookd.ErrSubscriptionNotFound))
		_, err = f.repo.GetDelivery(ctx, d.ID)
		assert.ErrorIs(t, err, hookd.ErrDeliveryNotFound)
		atts, err := f.repo.GetDeliveryAttempts(ctx, d.ID)
		require.NoError(t, err)
		assert.Empty(t, atts)
		live, err := f.repo.CheckIdempotency(ctx, d.IdempotencyKey, sub.ID)
		require.NoError(t, err)
		assert.False(t, live)
	})
}
