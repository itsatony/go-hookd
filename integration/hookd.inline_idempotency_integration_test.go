//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	hookd "github.com/itsatony/go-hookd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQueueInlineDelivery_IdempotencyKeyOnPostgres pins v0.11.1: a keyed inline
// delivery used to fail on PostgreSQL (the idempotency scope had an FK to
// subscriptions); a schema that still has that FK is fixed IN PLACE by
// EnsureSchema; a duplicate is refused per tenant+URL only.
func TestQueueInlineDelivery_IdempotencyKeyOnPostgres(t *testing.T) {
	dsn := setupClaimSchema(t)
	db := openDB(t, dsn)
	// Re-create the v0.11.0 FK, as an upgraded deployment still has it.
	_, err := db.Exec(fmt.Sprintf(`ALTER TABLE %[1]s_hookd_idempotency_store
		ADD CONSTRAINT fk_%[1]s_hookd_idempotency_subscription FOREIGN KEY (subscription_id)
		REFERENCES %[1]s_hookd_subscriptions(id) ON DELETE CASCADE`, claimPrefix))
	require.NoError(t, err)
	require.NoError(t, ensureConcurrently(t, dsn, claimPrefix, concurrentBooters)[0])
	var fk bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = $1)`,
		"fk_"+claimPrefix+"_hookd_idempotency_subscription").Scan(&fk))
	require.False(t, fk, "EnsureSchema must drop the obsolete FK in place")

	m, err := hookd.NewManager(hookd.NewConfig(dsn), newRepo(t, dsn))
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, m.Start(ctx))
	t.Cleanup(func() { _ = m.Stop() })

	long := "https://example.com/hook/" + strings.Repeat("a", 400) // longer than the scope column
	req := func(tenant string) *hookd.QueueInlineDeliveryRequest {
		return &hookd.QueueInlineDeliveryRequest{URL: long, EventType: "e", Payload: map[string]any{"x": 1},
			TenantID: tenant, IdempotencyKey: "outbox-row-1"}
	}
	d, err := m.QueueInlineDelivery(ctx, req("tenant_a"))
	require.NoError(t, err)
	assert.Equal(t, "outbox-row-1", d.IdempotencyKey)

	_, err = m.QueueInlineDelivery(ctx, req("tenant_a"))
	require.Error(t, err, "same tenant, URL and key is a duplicate")

	_, err = m.QueueInlineDelivery(ctx, req("tenant_b"))
	require.NoError(t, err, "another tenant's identical key must not be suppressed")
}

// TestStoreIdempotencyKey_RefusesLiveTakesOverExpired: before v0.11.1 the
// PostgreSQL store was an unconditional upsert, so no duplicate was ever refused.
func TestStoreIdempotencyKey_RefusesLiveTakesOverExpired(t *testing.T) {
	dsn := setupClaimSchema(t)
	repo := newRepo(t, dsn)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)
	require.NoError(t, repo.StoreIdempotencyKey(ctx, "k1", "sub_x", future))
	err := repo.StoreIdempotencyKey(ctx, "k1", "sub_x", future)
	require.Error(t, err)
	assert.True(t, hookd.IsConflictError(err))
	require.NoError(t, repo.StoreIdempotencyKey(ctx, "k1", "sub_y", future), "other scope")

	require.NoError(t, repo.StoreIdempotencyKey(ctx, "k2", "sub_x", time.Now().Add(-time.Minute)))
	require.NoError(t, repo.StoreIdempotencyKey(ctx, "k2", "sub_x", future), "an expired key is taken over")
	ok, err := repo.CheckIdempotency(ctx, "k2", "sub_x")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRedriveBudgetAndSubscriptionKeyErasureOnPostgres(t *testing.T) {
	dsn := setupClaimSchema(t)
	repo := newRepo(t, dsn)
	ctx := context.Background()
	ids := seedInline(t, repo, 1, "https://example.invalid/h", e2eTenant)
	for i := 1; i <= 3; i++ {
		cur, err := repo.GetDelivery(ctx, ids[0])
		require.NoError(t, err)
		cur.AttemptCount = cur.MaxAttempts
		require.NoError(t, repo.UpdateDelivery(ctx, cur))
		require.NoError(t, repo.MoveToDeadLetter(ctx, ids[0], "t"))
		again, err := repo.RequeueDeadLetter(ctx, ids[0])
		require.NoError(t, err)
		assert.Equal(t, 3*i+3, again.MaxAttempts, "redrive %d renews the original 3", i)
		assert.Equal(t, 3, again.AttemptBudget)
	}

	sub := &hookd.Subscription{ID: "sub_erase", TenantID: e2eTenant, URL: "https://example.invalid/s",
		Secret: "s3cr3t-s3cr3t-s3cr3t", EventTypes: []string{"e"}, Status: hookd.SubscriptionStatusActive,
		RetryPolicy: hookd.DefaultRetryPolicy()}
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	require.NoError(t, repo.StoreIdempotencyKey(ctx, "k-sub", sub.ID, time.Now().Add(time.Hour)))
	require.NoError(t, repo.DeleteSubscription(ctx, sub.ID))
	live, err := repo.CheckIdempotency(ctx, "k-sub", sub.ID)
	require.NoError(t, err)
	assert.False(t, live, "a deleted subscription's keys go with it")
}

// The obsolete FK is found by STRUCTURE, not by its spelled name (identifiers
// can be truncated or renamed): a renamed FK is dropped too.
func TestEnsureSchema_DropsObsoleteFKWhateverItsName(t *testing.T) {
	dsn := setupClaimSchema(t)
	db := openDB(t, dsn)
	_, err := db.Exec(fmt.Sprintf(`ALTER TABLE %[1]s_hookd_idempotency_store
		ADD CONSTRAINT "some other name" FOREIGN KEY (subscription_id)
		REFERENCES %[1]s_hookd_subscriptions(id)`, claimPrefix))
	require.NoError(t, err)
	require.NoError(t, ensureConcurrently(t, dsn, claimPrefix, 1)[0])
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM pg_constraint WHERE contype = 'f'
		AND conrelid = to_regclass($1)`, claimPrefix+"_hookd_idempotency_store").Scan(&n))
	assert.Equal(t, 0, n)
}
