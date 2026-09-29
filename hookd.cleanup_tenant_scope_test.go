package hookd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// These tests pin the v0.11.0 fail-closed tenant scope on the DESTRUCTIVE
// cleanup path (go-hookd#7). Each fails on v0.10.0, where an empty TenantID in a
// CleanupFilter silently dropped the tenant clause, so a dry run counted — and a
// real run deleted — every tenant's deliveries.

func TestCleanupFilter_RequireTenantScope(t *testing.T) {
	status := DeliveryStatusSuccess
	tests := []struct {
		name    string
		filter  *CleanupFilter
		wantErr bool
	}{
		{name: "nil filter refused", filter: nil, wantErr: true},
		{name: "empty tenant without opt-in refused", filter: &CleanupFilter{Status: &status}, wantErr: true},
		{name: "tenant set allowed", filter: &CleanupFilter{TenantID: "t1"}},
		{name: "AllTenants opt-in allowed", filter: &CleanupFilter{AllTenants: true}},
		{name: "tenant and AllTenants allowed (tenant wins)", filter: &CleanupFilter{TenantID: "t1", AllTenants: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.requireTenantScope()
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, IsValidationError(err))
				return
			}
			require.NoError(t, err)
		})
	}
}

// cleanupRepos returns every Count/Delete implementation the library ships. The
// Postgres ones carry no connection: the guard must refuse BEFORE any SQL runs,
// so a nil *sql.DB / *sql.Tx is never reached (it would panic if it were).
func cleanupRepos(t *testing.T) map[string]interface {
	CountDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error)
	DeleteDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error)
} {
	t.Helper()
	schema, err := NewSchemaConfig("scopetest")
	require.NoError(t, err)
	mock := NewMockRepository()
	mockTx, err := mock.BeginTx(context.Background())
	require.NoError(t, err)
	return map[string]interface {
		CountDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error)
		DeleteDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error)
	}{
		"postgres":    &PostgresRepository{pgStore: pgStore{schemaConfig: schema}},
		"postgres_tx": &PostgresRepositoryTx{pgStore: pgStore{schemaConfig: schema}},
		"mock":        mock,
		"mock_tx":     mockTx,
	}
}

func TestCleanupByFilter_EmptyTenantRefusedEverywhere(t *testing.T) {
	ctx := context.Background()
	cutoff := time.Now()
	filters := map[string]*CleanupFilter{
		"nil":                 nil,
		"created_before only": {CreatedBefore: &cutoff},
	}
	for repoName, repo := range cleanupRepos(t) {
		for filterName, filter := range filters {
			t.Run(repoName+"/"+filterName, func(t *testing.T) {
				_, err := repo.CountDeliveriesByFilter(ctx, filter)
				require.Error(t, err, "count must refuse")
				assert.True(t, IsValidationError(err))

				_, err = repo.DeleteDeliveriesByFilter(ctx, filter)
				require.Error(t, err, "delete must refuse")
				assert.True(t, IsValidationError(err))
			})
		}
	}
}

// TestManagerCleanup_TenantScoped proves the behaviour, not just the refusal: a
// tenant-scoped sweep leaves the other tenant's rows alone, a tenant-less one is
// refused (dry run AND real), and only AllTenants reaches every tenant.
func TestManagerCleanup_TenantScoped(t *testing.T) {
	ctx := context.Background()
	repo := NewMockRepository()
	manager, err := NewManager(NewConfig("postgres://test"), repo, WithLogger(zaptest.NewLogger(t)))
	require.NoError(t, err)
	defer func() { _ = manager.Stop() }()

	seedTwoTenantDeliveries(t, repo)
	cutoff := time.Now().Add(time.Hour)

	for _, dryRun := range []bool{true, false} {
		_, err = manager.CleanupDeliveries(ctx, &CleanupFilter{CreatedBefore: &cutoff}, dryRun)
		require.Error(t, err, "tenant-less cleanup must be refused (dryRun=%v)", dryRun)
		assert.True(t, IsValidationError(err))
	}

	res, err := manager.CleanupDeliveries(ctx, &CleanupFilter{CreatedBefore: &cutoff, TenantID: "tenant_b"}, false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.DeliveriesDeleted)

	remaining, err := repo.ListDeliveries(ctx, &DeliveryFilter{AllTenants: true})
	require.NoError(t, err)
	require.Len(t, remaining, 2)
	for _, d := range remaining {
		assert.Equal(t, "tenant_a", d.TenantID, "tenant_a rows must survive a tenant_b sweep")
	}

	res, err = manager.CleanupDeliveries(ctx, &CleanupFilter{CreatedBefore: &cutoff, AllTenants: true}, false)
	require.NoError(t, err)
	assert.Equal(t, int64(2), res.DeliveriesDeleted)
}
