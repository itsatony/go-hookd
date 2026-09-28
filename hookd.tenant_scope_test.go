package hookd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the v0.10.0 fail-closed tenant scope. Each one FAILS on the
// pre-v0.10.0 code, where an empty TenantID silently dropped the WHERE clause and
// returned every tenant's rows.
//
// The security defect they guard: a caller with an org-less identity (a bare S2S
// credential, charonmw >= v0.23 no longer promotes it to a system org) reached
// ListDeliveries with TenantID == "" and read the whole deliveries table,
// payloads included.

// requireTenantScope is the single predicate every List path shares.
func TestRequireTenantScope(t *testing.T) {
	t.Run("delivery: empty tenant, no opt-in -> refused", func(t *testing.T) {
		err := (&DeliveryFilter{}).requireTenantScope()
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
	t.Run("delivery: tenant set -> allowed", func(t *testing.T) {
		require.NoError(t, (&DeliveryFilter{TenantID: "t1"}).requireTenantScope())
	})
	t.Run("delivery: AllTenants opt-in -> allowed", func(t *testing.T) {
		require.NoError(t, (&DeliveryFilter{AllTenants: true}).requireTenantScope())
	})
	t.Run("subscription: empty tenant, no opt-in -> refused", func(t *testing.T) {
		err := (&SubscriptionFilter{}).requireTenantScope()
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
	})
	t.Run("subscription: tenant set -> allowed", func(t *testing.T) {
		require.NoError(t, (&SubscriptionFilter{TenantID: "t1"}).requireTenantScope())
	})
	t.Run("subscription: AllTenants opt-in -> allowed", func(t *testing.T) {
		require.NoError(t, (&SubscriptionFilter{AllTenants: true}).requireTenantScope())
	})
}

// seedTwoTenantDeliveries writes two deliveries for tenant_a and one for
// tenant_b directly into a mock repository, so a cross-tenant leak is visible as
// a row from the other tenant.
func seedTwoTenantDeliveries(t *testing.T, repo *MockRepository) {
	t.Helper()
	ctx := context.Background()
	subA := createTestSubscription(t, "sub_a", "tenant_a", "https://a.example.com/webhook")
	subB := createTestSubscription(t, "sub_b", "tenant_b", "https://b.example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, subA))
	require.NoError(t, repo.CreateSubscription(ctx, subB))
	require.NoError(t, repo.CreateDelivery(ctx, createTestDelivery(t, "dlv_a1", "sub_a", "tenant_a")))
	require.NoError(t, repo.CreateDelivery(ctx, createTestDelivery(t, "dlv_a2", "sub_a", "tenant_a")))
	require.NoError(t, repo.CreateDelivery(ctx, createTestDelivery(t, "dlv_b1", "sub_b", "tenant_b")))
}

func TestManagerListDeliveries_TenantScope(t *testing.T) {
	ctx := context.Background()

	newMgr := func(t *testing.T) (*Manager, *MockRepository) {
		t.Helper()
		repo := NewMockRepository()
		mgr, err := NewManager(NewConfig("postgres://localhost/test"), repo)
		require.NoError(t, err)
		return mgr, repo
	}

	t.Run("empty org is refused, not answered with every tenant", func(t *testing.T) {
		mgr, repo := newMgr(t)
		seedTwoTenantDeliveries(t, repo)

		deliveries, err := mgr.ListDeliveries(ctx, &DeliveryFilter{TenantID: "", Limit: 100})

		// Pre-v0.10.0 this returned (3 rows across two tenants, nil) — the leak.
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
		assert.Nil(t, deliveries)
	})

	t.Run("a tenant sees only its own rows", func(t *testing.T) {
		mgr, repo := newMgr(t)
		seedTwoTenantDeliveries(t, repo)

		deliveries, err := mgr.ListDeliveries(ctx, &DeliveryFilter{TenantID: "tenant_a", Limit: 100})
		require.NoError(t, err)
		assert.Len(t, deliveries, 2)
		for _, d := range deliveries {
			assert.Equal(t, "tenant_a", d.TenantID)
		}
	})

	t.Run("AllTenants opt-in lists across tenants", func(t *testing.T) {
		mgr, repo := newMgr(t)
		seedTwoTenantDeliveries(t, repo)

		deliveries, err := mgr.ListDeliveries(ctx, &DeliveryFilter{AllTenants: true, Limit: 100})
		require.NoError(t, err)
		assert.Len(t, deliveries, 3)
	})
}

func TestManagerListSubscriptions_TenantScope(t *testing.T) {
	ctx := context.Background()
	repo := NewMockRepository()
	mgr, err := NewManager(NewConfig("postgres://localhost/test"), repo)
	require.NoError(t, err)

	require.NoError(t, repo.CreateSubscription(ctx, createTestSubscription(t, "s_a", "tenant_a", "https://a.example.com/webhook")))
	require.NoError(t, repo.CreateSubscription(ctx, createTestSubscription(t, "s_b", "tenant_b", "https://b.example.com/webhook")))

	t.Run("empty org is refused", func(t *testing.T) {
		subs, err := mgr.ListSubscriptions(ctx, &SubscriptionFilter{TenantID: "", Limit: 100})
		require.Error(t, err)
		assert.True(t, IsValidationError(err))
		assert.Nil(t, subs)
	})

	t.Run("a tenant sees only its own", func(t *testing.T) {
		subs, err := mgr.ListSubscriptions(ctx, &SubscriptionFilter{TenantID: "tenant_a", Limit: 100})
		require.NoError(t, err)
		assert.Len(t, subs, 1)
		assert.Equal(t, "tenant_a", subs[0].TenantID)
	})

	t.Run("AllTenants opt-in lists across tenants", func(t *testing.T) {
		subs, err := mgr.ListSubscriptions(ctx, &SubscriptionFilter{AllTenants: true, Limit: 100})
		require.NoError(t, err)
		assert.Len(t, subs, 2)
	})
}
