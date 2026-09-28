package hookd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// =============================================================================
// CLEANUP FILTER VALIDATION TESTS
// =============================================================================

func TestCleanupFilter_Validate(t *testing.T) {
	tests := []struct {
		name    string
		filter  *CleanupFilter
		wantErr bool
	}{
		{
			name:    "empty filter should fail",
			filter:  &CleanupFilter{},
			wantErr: true,
		},
		{
			name:    "nil filter should fail",
			filter:  nil,
			wantErr: true,
		},
		{
			name:    "status without tenant or AllTenants should fail (v0.11.0 fail-closed)",
			filter:  &CleanupFilter{Status: ptr(DeliveryStatusSuccess)},
			wantErr: true,
		},
		{
			name:    "AllTenants alone is not a constraint and should fail",
			filter:  &CleanupFilter{AllTenants: true},
			wantErr: true,
		},
		{
			name: "filter with only status should succeed",
			filter: &CleanupFilter{
				Status:     ptr(DeliveryStatusSuccess),
				AllTenants: true,
			},
			wantErr: false,
		},
		{
			name: "filter with only tenant_id should succeed",
			filter: &CleanupFilter{
				TenantID: "tenant-123",
			},
			wantErr: false,
		},
		{
			name: "filter with only created_before should succeed",
			filter: &CleanupFilter{
				CreatedBefore: ptr(time.Now()),
				AllTenants:    true,
			},
			wantErr: false,
		},
		{
			name: "filter with invalid time range should fail",
			filter: &CleanupFilter{
				CreatedBefore: ptr(time.Now().Add(-24 * time.Hour)),
				CreatedAfter:  ptr(time.Now()),
				AllTenants:    true,
			},
			wantErr: true,
		},
		{
			name: "filter with valid time range should succeed",
			filter: &CleanupFilter{
				CreatedBefore: ptr(time.Now()),
				CreatedAfter:  ptr(time.Now().Add(-24 * time.Hour)),
				TenantID:      "tenant-123",
			},
			wantErr: false,
		},
		{
			name: "filter with multiple constraints should succeed",
			filter: &CleanupFilter{
				Status:        ptr(DeliveryStatusSuccess),
				TenantID:      "tenant-123",
				CreatedBefore: ptr(time.Now()),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filter.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// =============================================================================
// MAINTENANCE STATS TESTS
// =============================================================================

func TestManager_GetMaintenanceStats(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := NewMockRepository()
	config := NewConfig("postgres://test")

	manager, err := NewManager(config, repo, WithLogger(logger))
	require.NoError(t, err)
	defer manager.Stop()

	ctx := context.Background()

	// Create some test data
	sub := makeTestSubscription("tenant-1", "http://example.com/webhook")
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create deliveries with different statuses
	now := time.Now()
	deliveries := []*Delivery{
		{ID: "dlv_1", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "dlv_2", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now.Add(-24 * time.Hour)},
		{ID: "dlv_3", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusFailed, MaxAttempts: 3, CreatedAt: now.Add(-12 * time.Hour)},
		{ID: "dlv_4", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusDeadLetter, MaxAttempts: 3, CreatedAt: now.Add(-6 * time.Hour)},
		{ID: "dlv_5", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusPending, MaxAttempts: 3, CreatedAt: now},
	}

	for _, d := range deliveries {
		err = repo.CreateDelivery(ctx, d)
		require.NoError(t, err)
	}

	// Create some delivery attempts
	for i := 0; i < 3; i++ {
		err = repo.CreateDeliveryAttempt(ctx, &DeliveryAttempt{
			ID:            "att_" + string(rune('a'+i)),
			DeliveryID:    "dlv_1",
			AttemptNumber: i + 1,
			StatusCode:    200,
			AttemptedAt:   now,
		})
		require.NoError(t, err)
	}

	// Store an expired idempotency key
	err = repo.StoreIdempotencyKey(ctx, "expired-key", sub.ID, time.Now().Add(-1*time.Hour))
	require.NoError(t, err)

	// Store a valid idempotency key
	err = repo.StoreIdempotencyKey(ctx, "valid-key", sub.ID, time.Now().Add(1*time.Hour))
	require.NoError(t, err)

	// Get maintenance stats
	stats, err := manager.GetMaintenanceStats(ctx)
	require.NoError(t, err)

	// Verify stats
	assert.Equal(t, int64(5), stats.TotalDeliveries)
	assert.Equal(t, int64(3), stats.TotalDeliveryAttempts)
	assert.Equal(t, int64(2), stats.IdempotencyKeys)
	assert.Equal(t, int64(1), stats.ExpiredIdempotencyKeys)

	// Verify breakdown by status
	assert.Equal(t, int64(2), stats.DeliveriesByStatus[DeliveryStatusSuccess])
	assert.Equal(t, int64(1), stats.DeliveriesByStatus[DeliveryStatusFailed])
	assert.Equal(t, int64(1), stats.DeliveriesByStatus[DeliveryStatusDeadLetter])
	assert.Equal(t, int64(1), stats.DeliveriesByStatus[DeliveryStatusPending])

	// Verify timestamps
	assert.NotNil(t, stats.OldestDeliveryAt)
	assert.NotNil(t, stats.NewestDeliveryAt)
}

// =============================================================================
// DELIVERY CLEANUP TESTS
// =============================================================================

func TestManager_CleanupDeliveries_DryRun(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := NewMockRepository()
	config := NewConfig("postgres://test")

	manager, err := NewManager(config, repo, WithLogger(logger))
	require.NoError(t, err)
	defer manager.Stop()

	ctx := context.Background()

	// Create test data
	sub := makeTestSubscription("tenant-1", "http://example.com/webhook")
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	now := time.Now()
	deliveries := []*Delivery{
		{ID: "dlv_old_1", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "dlv_old_2", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now.Add(-36 * time.Hour)},
		{ID: "dlv_new", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now},
	}

	for _, d := range deliveries {
		err = repo.CreateDelivery(ctx, d)
		require.NoError(t, err)
	}

	// Dry-run cleanup of old successful deliveries
	cutoff := now.Add(-24 * time.Hour)
	status := DeliveryStatusSuccess
	filter := &CleanupFilter{
		Status:        &status,
		CreatedBefore: &cutoff,
		TenantID:      "tenant-1",
	}

	result, err := manager.CleanupDeliveries(ctx, filter, true)
	require.NoError(t, err)

	// Should report 2 deliveries would be deleted
	assert.Equal(t, int64(2), result.DeliveriesDeleted)
	assert.True(t, result.DryRun)

	// Verify nothing was actually deleted
	stats, err := manager.GetMaintenanceStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.TotalDeliveries)
}

func TestManager_CleanupDeliveries_Actual(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := NewMockRepository()
	config := NewConfig("postgres://test")

	manager, err := NewManager(config, repo, WithLogger(logger))
	require.NoError(t, err)
	defer manager.Stop()

	ctx := context.Background()

	// Create test data
	sub := makeTestSubscription("tenant-1", "http://example.com/webhook")
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	now := time.Now()
	deliveries := []*Delivery{
		{ID: "dlv_old_1", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "dlv_old_2", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now.Add(-36 * time.Hour)},
		{ID: "dlv_new", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now},
	}

	for _, d := range deliveries {
		err = repo.CreateDelivery(ctx, d)
		require.NoError(t, err)
	}

	// Actual cleanup
	cutoff := now.Add(-24 * time.Hour)
	status := DeliveryStatusSuccess
	filter := &CleanupFilter{
		Status:        &status,
		CreatedBefore: &cutoff,
		AllTenants:    true,
	}

	result, err := manager.CleanupDeliveries(ctx, filter, false)
	require.NoError(t, err)

	// Should have deleted 2 deliveries
	assert.Equal(t, int64(2), result.DeliveriesDeleted)
	assert.False(t, result.DryRun)

	// Verify actual deletion
	stats, err := manager.GetMaintenanceStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.TotalDeliveries)
}

func TestManager_CleanupDeliveries_ValidationError(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := NewMockRepository()
	config := NewConfig("postgres://test")

	manager, err := NewManager(config, repo, WithLogger(logger))
	require.NoError(t, err)
	defer manager.Stop()

	ctx := context.Background()

	// Try to cleanup with empty filter (should fail)
	filter := &CleanupFilter{}

	_, err = manager.CleanupDeliveries(ctx, filter, true)
	assert.Error(t, err)
}

// =============================================================================
// IDEMPOTENCY CLEANUP TESTS
// =============================================================================

func TestManager_CleanupIdempotencyKeys_DryRun(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := NewMockRepository()
	config := NewConfig("postgres://test")

	manager, err := NewManager(config, repo, WithLogger(logger))
	require.NoError(t, err)
	defer manager.Stop()

	ctx := context.Background()

	// Create subscription
	sub := makeTestSubscription("tenant-1", "http://example.com/webhook")
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Store some idempotency keys
	err = repo.StoreIdempotencyKey(ctx, "expired-1", sub.ID, time.Now().Add(-2*time.Hour))
	require.NoError(t, err)
	err = repo.StoreIdempotencyKey(ctx, "expired-2", sub.ID, time.Now().Add(-1*time.Hour))
	require.NoError(t, err)
	err = repo.StoreIdempotencyKey(ctx, "valid", sub.ID, time.Now().Add(1*time.Hour))
	require.NoError(t, err)

	// Dry-run cleanup
	result, err := manager.CleanupIdempotencyKeys(ctx, true)
	require.NoError(t, err)

	// Should report 2 keys would be deleted
	assert.Equal(t, int64(2), result.KeysDeleted)

	// Verify nothing was actually deleted
	stats, err := manager.GetMaintenanceStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), stats.IdempotencyKeys)
}

func TestManager_CleanupIdempotencyKeys_Actual(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := NewMockRepository()
	config := NewConfig("postgres://test")

	manager, err := NewManager(config, repo, WithLogger(logger))
	require.NoError(t, err)
	defer manager.Stop()

	ctx := context.Background()

	// Create subscription
	sub := makeTestSubscription("tenant-1", "http://example.com/webhook")
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Store some idempotency keys
	err = repo.StoreIdempotencyKey(ctx, "expired-1", sub.ID, time.Now().Add(-2*time.Hour))
	require.NoError(t, err)
	err = repo.StoreIdempotencyKey(ctx, "expired-2", sub.ID, time.Now().Add(-1*time.Hour))
	require.NoError(t, err)
	err = repo.StoreIdempotencyKey(ctx, "valid", sub.ID, time.Now().Add(1*time.Hour))
	require.NoError(t, err)

	// Actual cleanup
	result, err := manager.CleanupIdempotencyKeys(ctx, false)
	require.NoError(t, err)

	// Should have deleted 2 keys
	assert.Equal(t, int64(2), result.KeysDeleted)

	// Verify actual deletion
	stats, err := manager.GetMaintenanceStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.IdempotencyKeys)
	assert.Equal(t, int64(0), stats.ExpiredIdempotencyKeys)
}

// =============================================================================
// CONVENIENCE METHOD TESTS
// =============================================================================

func TestManager_CleanupSuccessfulDeliveries(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := NewMockRepository()
	config := NewConfig("postgres://test")

	manager, err := NewManager(config, repo, WithLogger(logger))
	require.NoError(t, err)
	defer manager.Stop()

	ctx := context.Background()

	// Create test data
	sub := makeTestSubscription("tenant-1", "http://example.com/webhook")
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	now := time.Now()
	deliveries := []*Delivery{
		{ID: "dlv_success_old", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "dlv_failed_old", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusFailed, MaxAttempts: 3, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "dlv_success_new", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now},
	}

	for _, d := range deliveries {
		err = repo.CreateDelivery(ctx, d)
		require.NoError(t, err)
	}

	// Cleanup successful deliveries older than 24 hours
	result, err := manager.CleanupSuccessfulDeliveries(ctx, 24*time.Hour, false)
	require.NoError(t, err)

	// Should have deleted only 1 (old successful) delivery
	assert.Equal(t, int64(1), result.DeliveriesDeleted)

	// Verify: 2 remain (1 failed, 1 new successful)
	stats, err := manager.GetMaintenanceStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.TotalDeliveries)
}

func TestManager_CleanupAllCompletedDeliveries(t *testing.T) {
	logger := zaptest.NewLogger(t)
	repo := NewMockRepository()
	config := NewConfig("postgres://test")

	manager, err := NewManager(config, repo, WithLogger(logger))
	require.NoError(t, err)
	defer manager.Stop()

	ctx := context.Background()

	// Create test data
	sub := makeTestSubscription("tenant-1", "http://example.com/webhook")
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	now := time.Now()
	deliveries := []*Delivery{
		{ID: "dlv_success_old", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusSuccess, MaxAttempts: 3, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "dlv_failed_old", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusFailed, MaxAttempts: 3, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "dlv_deadletter_old", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusDeadLetter, MaxAttempts: 3, CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "dlv_pending_new", SubscriptionID: sub.ID, TenantID: "tenant-1", EventType: "test.event", Payload: map[string]any{}, Status: DeliveryStatusPending, MaxAttempts: 3, CreatedAt: now},
	}

	for _, d := range deliveries {
		err = repo.CreateDelivery(ctx, d)
		require.NoError(t, err)
	}

	// Cleanup all completed deliveries older than 24 hours
	// This should cleanup success and failed, but NOT dead_letter
	result, err := manager.CleanupAllCompletedDeliveries(ctx, 24*time.Hour, false)
	require.NoError(t, err)

	// Should have deleted 2 (success + failed)
	assert.Equal(t, int64(2), result.DeliveriesDeleted)

	// Verify: 2 remain (dead_letter + pending)
	stats, err := manager.GetMaintenanceStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.TotalDeliveries)
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

func ptr[T any](v T) *T {
	return &v
}

func makeTestSubscription(tenantID, url string) *Subscription {
	return &Subscription{
		ID:         "sub_test_" + tenantID,
		TenantID:   tenantID,
		URL:        url,
		Secret:     "test-secret-12345",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
		RetryPolicy: &RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: time.Second,
			MaxBackoff:     time.Hour,
			BackoffFactor:  2.0,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}
