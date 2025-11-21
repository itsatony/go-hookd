// Package internal provides the core webhook management implementation for go-hookd.
//
// This file contains comprehensive unit tests for the MockRepository implementation.
// Tests verify all Repository interface methods work correctly with in-memory storage.
package internal

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

func createTestSubscription(t *testing.T, id, tenantID, url string) *Subscription {
	t.Helper()

	return &Subscription{
		ID:         id,
		TenantID:   tenantID,
		URL:        url,
		Secret:     "test-secret-" + id,
		EventTypes: []string{"user.created", "user.updated"},
		Status:     SubscriptionStatusActive,
		RetryPolicy: &RetryPolicy{
			MaxAttempts:    10,
			InitialBackoff: 1 * time.Second,
			MaxBackoff:     1 * time.Hour,
			BackoffFactor:  2.0,
		},
		Headers: map[string]string{
			"X-Custom-Header": "value",
		},
		Metadata: map[string]interface{}{
			"key": "value",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

func createTestDelivery(t *testing.T, id, subscriptionID, tenantID string) *Delivery {
	t.Helper()

	return &Delivery{
		ID:             id,
		SubscriptionID: subscriptionID,
		TenantID:       tenantID,
		EventType:      "user.created",
		Payload: map[string]interface{}{
			"user_id": "12345",
			"email":   "test@example.com",
		},
		Status:       DeliveryStatusPending,
		AttemptCount: 0,
		MaxAttempts:  10,
		NextRetryAt:  nil,
		CompletedAt:  nil,
		CreatedAt:    time.Now(),
	}
}

func createTestDeliveryAttempt(t *testing.T, id, deliveryID string, attemptNumber int) *DeliveryAttempt {
	t.Helper()

	return &DeliveryAttempt{
		ID:            id,
		DeliveryID:    deliveryID,
		AttemptNumber: attemptNumber,
		StatusCode:    200,
		ResponseBody:  "OK",
		ResponseHeaders: map[string]string{
			"Content-Type": "application/json",
		},
		Error:       "",
		DurationMs:  150,
		AttemptedAt: time.Now(),
	}
}

// =============================================================================
// SUBSCRIPTION TESTS
// =============================================================================

func TestMockRepository_CreateSubscription(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")

	err := repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Verify subscription was created
	retrieved, err := repo.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, retrieved.ID)
	assert.Equal(t, sub.TenantID, retrieved.TenantID)
	assert.Equal(t, sub.URL, retrieved.URL)
}

func TestMockRepository_CreateSubscription_Duplicate(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")

	// Create first subscription
	err := repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Try to create duplicate (same tenant + URL)
	sub2 := createTestSubscription(t, "sub_test2", "tenant1", "https://example.com/webhook")
	err = repo.CreateSubscription(ctx, sub2)
	assert.ErrorIs(t, err, ErrDuplicateSubscription)
}

func TestMockRepository_GetSubscription_NotFound(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	_, err := repo.GetSubscription(ctx, "sub_nonexistent")
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestMockRepository_GetSubscriptionByTenantAndURL(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	err := repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Find by tenant + URL
	retrieved, err := repo.GetSubscriptionByTenantAndURL(ctx, "tenant1", "https://example.com/webhook")
	require.NoError(t, err)
	assert.Equal(t, sub.ID, retrieved.ID)

	// Not found
	_, err = repo.GetSubscriptionByTenantAndURL(ctx, "tenant1", "https://other.com/webhook")
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestMockRepository_UpdateSubscription(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	err := repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Update subscription
	sub.Status = SubscriptionStatusPaused
	sub.URL = "https://example.com/webhook2"

	err = repo.UpdateSubscription(ctx, sub)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionStatusPaused, retrieved.Status)
	assert.Equal(t, "https://example.com/webhook2", retrieved.URL)
}

func TestMockRepository_UpdateSubscription_NotFound(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_nonexistent", "tenant1", "https://example.com/webhook")
	err := repo.UpdateSubscription(ctx, sub)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestMockRepository_DeleteSubscription(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	err := repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Delete subscription
	err = repo.DeleteSubscription(ctx, sub.ID)
	require.NoError(t, err)

	// Verify deletion
	_, err = repo.GetSubscription(ctx, sub.ID)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestMockRepository_DeleteSubscription_Cascade(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	err := repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery for subscription
	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	err = repo.CreateDelivery(ctx, dlv)
	require.NoError(t, err)

	// Delete subscription (should cascade delete delivery)
	err = repo.DeleteSubscription(ctx, sub.ID)
	require.NoError(t, err)

	// Verify delivery was deleted
	_, err = repo.GetDelivery(ctx, dlv.ID)
	assert.ErrorIs(t, err, ErrDeliveryNotFound)
}

func TestMockRepository_ListSubscriptions(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Create multiple subscriptions
	sub1 := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook1")
	sub2 := createTestSubscription(t, "sub_test2", "tenant1", "https://example.com/webhook2")
	sub3 := createTestSubscription(t, "sub_test3", "tenant2", "https://example.com/webhook3")
	sub3.Status = SubscriptionStatusPaused

	require.NoError(t, repo.CreateSubscription(ctx, sub1))
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	require.NoError(t, repo.CreateSubscription(ctx, sub2))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, repo.CreateSubscription(ctx, sub3))

	t.Run("All subscriptions", func(t *testing.T) {
		filter := &SubscriptionFilter{Limit: 100}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 3)
		// Should be sorted by created_at descending
		assert.Equal(t, "sub_test3", subs[0].ID)
		assert.Equal(t, "sub_test2", subs[1].ID)
		assert.Equal(t, "sub_test1", subs[2].ID)
	})

	t.Run("Filter by tenant", func(t *testing.T) {
		filter := &SubscriptionFilter{TenantID: "tenant1", Limit: 100}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 2)
		assert.Equal(t, "sub_test2", subs[0].ID)
		assert.Equal(t, "sub_test1", subs[1].ID)
	})

	t.Run("Filter by status", func(t *testing.T) {
		filter := &SubscriptionFilter{Status: SubscriptionStatusPaused, Limit: 100}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 1)
		assert.Equal(t, "sub_test3", subs[0].ID)
	})

	t.Run("Filter by event types", func(t *testing.T) {
		filter := &SubscriptionFilter{EventTypes: []string{"user.created"}, Limit: 100}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 3) // All have user.created
	})

	t.Run("Limit and offset", func(t *testing.T) {
		filter := &SubscriptionFilter{Limit: 2, Offset: 0}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 2)
		assert.Equal(t, "sub_test3", subs[0].ID)

		filter = &SubscriptionFilter{Limit: 2, Offset: 1}
		subs, err = repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 2)
		assert.Equal(t, "sub_test2", subs[0].ID)
	})
}

// =============================================================================
// DELIVERY TESTS
// =============================================================================

func TestMockRepository_CreateDelivery(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	err := repo.CreateDelivery(ctx, dlv)
	require.NoError(t, err)

	// Verify delivery was created
	retrieved, err := repo.GetDelivery(ctx, dlv.ID)
	require.NoError(t, err)
	assert.Equal(t, dlv.ID, retrieved.ID)
	assert.Equal(t, dlv.SubscriptionID, retrieved.SubscriptionID)
}

func TestMockRepository_GetDelivery_NotFound(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	_, err := repo.GetDelivery(ctx, "dlv_nonexistent")
	assert.ErrorIs(t, err, ErrDeliveryNotFound)
}

func TestMockRepository_UpdateDelivery(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	// Update delivery
	dlv.Status = DeliveryStatusSuccess
	dlv.AttemptCount = 1
	now := time.Now()
	dlv.CompletedAt = &now

	err := repo.UpdateDelivery(ctx, dlv)
	require.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetDelivery(ctx, dlv.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, retrieved.Status)
	assert.Equal(t, 1, retrieved.AttemptCount)
	assert.NotNil(t, retrieved.CompletedAt)
}

func TestMockRepository_GetPendingDeliveries(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	// Create multiple deliveries with different states
	dlv1 := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	dlv2 := createTestDelivery(t, "dlv_test2", sub.ID, sub.TenantID)
	dlv3 := createTestDelivery(t, "dlv_test3", sub.ID, sub.TenantID)
	dlv3.Status = DeliveryStatusSuccess

	futureRetry := time.Now().Add(1 * time.Hour)
	dlv4 := createTestDelivery(t, "dlv_test4", sub.ID, sub.TenantID)
	dlv4.NextRetryAt = &futureRetry

	require.NoError(t, repo.CreateDelivery(ctx, dlv1))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, repo.CreateDelivery(ctx, dlv2))
	require.NoError(t, repo.CreateDelivery(ctx, dlv3))
	require.NoError(t, repo.CreateDelivery(ctx, dlv4))

	t.Run("Get pending deliveries", func(t *testing.T) {
		deliveries, err := repo.GetPendingDeliveries(ctx, 10)
		require.NoError(t, err)
		assert.Len(t, deliveries, 2) // dlv1 and dlv2 (dlv3 is success, dlv4 is scheduled for future)
		assert.Equal(t, "dlv_test1", deliveries[0].ID)
		assert.Equal(t, "dlv_test2", deliveries[1].ID)
	})

	t.Run("SKIP LOCKED simulation", func(t *testing.T) {
		// Unlock all deliveries from previous test
		repo.UnlockAllDeliveries()

		// Get pending deliveries (should lock dlv1)
		deliveries, err := repo.GetPendingDeliveries(ctx, 1)
		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.Equal(t, "dlv_test1", deliveries[0].ID)

		// Try to get again (should skip locked dlv1 and return dlv2)
		deliveries, err = repo.GetPendingDeliveries(ctx, 1)
		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.Equal(t, "dlv_test2", deliveries[0].ID)

		// Unlock all and try again (should return dlv1 since it's oldest)
		repo.UnlockAllDeliveries()
		deliveries, err = repo.GetPendingDeliveries(ctx, 1)
		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.Equal(t, "dlv_test1", deliveries[0].ID)
	})

	t.Run("Limit", func(t *testing.T) {
		repo.UnlockAllDeliveries()
		deliveries, err := repo.GetPendingDeliveries(ctx, 1)
		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
	})
}

func TestMockRepository_MoveToDeadLetter(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	// Move to dead letter
	err := repo.MoveToDeadLetter(ctx, dlv.ID, "max retries exceeded")
	require.NoError(t, err)

	// Verify status
	retrieved, err := repo.GetDelivery(ctx, dlv.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusDeadLetter, retrieved.Status)
	assert.NotNil(t, retrieved.CompletedAt)
}

// =============================================================================
// DELIVERY ATTEMPT TESTS
// =============================================================================

func TestMockRepository_CreateDeliveryAttempt(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	att := createTestDeliveryAttempt(t, "att_test1", dlv.ID, 1)
	err := repo.CreateDeliveryAttempt(ctx, att)
	require.NoError(t, err)

	// Verify attempt was created
	attempts, err := repo.GetDeliveryAttempts(ctx, dlv.ID)
	require.NoError(t, err)
	assert.Len(t, attempts, 1)
	assert.Equal(t, att.ID, attempts[0].ID)
}

func TestMockRepository_CreateDeliveryAttempt_Duplicate(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	att1 := createTestDeliveryAttempt(t, "att_test1", dlv.ID, 1)
	require.NoError(t, repo.CreateDeliveryAttempt(ctx, att1))

	// Try to create duplicate attempt number
	att2 := createTestDeliveryAttempt(t, "att_test2", dlv.ID, 1)
	err := repo.CreateDeliveryAttempt(ctx, att2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestMockRepository_GetDeliveryAttempts(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	// Create multiple attempts
	att1 := createTestDeliveryAttempt(t, "att_test1", dlv.ID, 1)
	att2 := createTestDeliveryAttempt(t, "att_test2", dlv.ID, 2)
	att3 := createTestDeliveryAttempt(t, "att_test3", dlv.ID, 3)

	require.NoError(t, repo.CreateDeliveryAttempt(ctx, att3)) // Create out of order
	require.NoError(t, repo.CreateDeliveryAttempt(ctx, att1))
	require.NoError(t, repo.CreateDeliveryAttempt(ctx, att2))

	// Get attempts (should be sorted by attempt_number)
	attempts, err := repo.GetDeliveryAttempts(ctx, dlv.ID)
	require.NoError(t, err)
	assert.Len(t, attempts, 3)
	assert.Equal(t, 1, attempts[0].AttemptNumber)
	assert.Equal(t, 2, attempts[1].AttemptNumber)
	assert.Equal(t, 3, attempts[2].AttemptNumber)
}

func TestMockRepository_GetDeliveryAttempts_Empty(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	attempts, err := repo.GetDeliveryAttempts(ctx, "dlv_nonexistent")
	require.NoError(t, err)
	assert.Empty(t, attempts)
}

// =============================================================================
// IDEMPOTENCY TESTS
// =============================================================================

func TestMockRepository_Idempotency(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	t.Run("Store and check idempotency key", func(t *testing.T) {
		expiresAt := time.Now().Add(1 * time.Hour)
		err := repo.StoreIdempotencyKey(ctx, "key1", "sub_test1", expiresAt)
		require.NoError(t, err)

		// Check key exists
		exists, err := repo.CheckIdempotency(ctx, "key1", "sub_test1")
		require.NoError(t, err)
		assert.True(t, exists)

		// Check different subscription
		exists, err = repo.CheckIdempotency(ctx, "key1", "sub_test2")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Expired key", func(t *testing.T) {
		expiresAt := time.Now().Add(-1 * time.Hour) // Expired
		err := repo.StoreIdempotencyKey(ctx, "key2", "sub_test1", expiresAt)
		require.NoError(t, err)

		// Check expired key (should return false)
		exists, err := repo.CheckIdempotency(ctx, "key2", "sub_test1")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Cleanup expired keys", func(t *testing.T) {
		// Store multiple keys with different expiration
		err := repo.StoreIdempotencyKey(ctx, "key3", "sub_test1", time.Now().Add(-1*time.Hour))
		require.NoError(t, err)
		err = repo.StoreIdempotencyKey(ctx, "key4", "sub_test1", time.Now().Add(1*time.Hour))
		require.NoError(t, err)

		// Cleanup
		count := repo.CleanupExpiredIdempotencyKeys()
		assert.GreaterOrEqual(t, count, 1) // At least key2 and key3
	})
}

// =============================================================================
// CIRCUIT BREAKER TESTS
// =============================================================================

func TestMockRepository_CircuitBreaker(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	t.Run("Get default state", func(t *testing.T) {
		state, err := repo.GetCircuitBreakerState(ctx, "https://example.com/webhook")
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, state.State)
		assert.Equal(t, 0, state.FailureCount)
	})

	t.Run("Update state", func(t *testing.T) {
		state := &CircuitBreakerState{
			Endpoint:     "https://example.com/webhook",
			State:        CircuitBreakerStateOpen,
			FailureCount: 5,
			SuccessCount: 0,
			LastFailure:  time.Now(),
			OpenedAt:     time.Now(),
			NextRetryAt:  time.Now().Add(1 * time.Minute),
		}

		err := repo.UpdateCircuitBreakerState(ctx, state)
		require.NoError(t, err)

		// Verify update
		retrieved, err := repo.GetCircuitBreakerState(ctx, "https://example.com/webhook")
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateOpen, retrieved.State)
		assert.Equal(t, 5, retrieved.FailureCount)
	})
}

// =============================================================================
// TRANSACTION TESTS
// =============================================================================

func TestMockRepository_Transaction_Commit(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// Create subscription in transaction
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	err = tx.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Verify subscription exists in transaction
	retrieved, err := tx.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, retrieved.ID)

	// Verify subscription does NOT exist in parent repo yet
	_, err = repo.GetSubscription(ctx, sub.ID)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)

	// Commit transaction
	err = tx.Commit()
	require.NoError(t, err)

	// Verify subscription NOW exists in parent repo
	retrieved, err = repo.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, sub.ID, retrieved.ID)
}

func TestMockRepository_Transaction_Rollback(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// Create subscription in transaction
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	err = tx.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Rollback transaction
	err = tx.Rollback()
	require.NoError(t, err)

	// Verify subscription does NOT exist in parent repo
	_, err = repo.GetSubscription(ctx, sub.ID)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestMockRepository_Transaction_MultipleOperations(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// Create subscription
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	err = tx.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery
	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	err = tx.CreateDelivery(ctx, dlv)
	require.NoError(t, err)

	// Create delivery attempt
	att := createTestDeliveryAttempt(t, "att_test1", dlv.ID, 1)
	err = tx.CreateDeliveryAttempt(ctx, att)
	require.NoError(t, err)

	// Commit
	err = tx.Commit()
	require.NoError(t, err)

	// Verify all entities exist in parent repo
	_, err = repo.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)

	_, err = repo.GetDelivery(ctx, dlv.ID)
	require.NoError(t, err)

	attempts, err := repo.GetDeliveryAttempts(ctx, dlv.ID)
	require.NoError(t, err)
	assert.Len(t, attempts, 1)
}

func TestMockRepository_Transaction_Isolation(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Create initial subscription in parent repo
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	err := repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// Update subscription in transaction
	sub.Status = SubscriptionStatusPaused
	err = tx.UpdateSubscription(ctx, sub)
	require.NoError(t, err)

	// Verify update in transaction
	txSub, err := tx.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionStatusPaused, txSub.Status)

	// Verify parent repo still has original value
	parentSub, err := repo.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionStatusActive, parentSub.Status)

	// Commit
	err = tx.Commit()
	require.NoError(t, err)

	// Verify parent repo now has updated value
	parentSub, err = repo.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionStatusPaused, parentSub.Status)
}

// =============================================================================
// HEALTH AND MAINTENANCE TESTS
// =============================================================================

func TestMockRepository_Ping(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	err := repo.Ping(ctx)
	assert.NoError(t, err)
}

func TestMockRepository_Close(t *testing.T) {
	repo := NewMockRepository()

	err := repo.Close()
	assert.NoError(t, err)
}

// =============================================================================
// MOCK REPOSITORY TX TESTS (Transaction-specific operations)
// =============================================================================

func TestMockRepositoryTx_GetSubscriptionByTenantAndURL(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Create subscription in parent repo
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	t.Run("Find existing subscription", func(t *testing.T) {
		retrieved, err := tx.GetSubscriptionByTenantAndURL(ctx, "tenant1", "https://example.com/webhook")
		require.NoError(t, err)
		assert.Equal(t, sub.ID, retrieved.ID)
	})

	t.Run("Not found - different tenant", func(t *testing.T) {
		_, err := tx.GetSubscriptionByTenantAndURL(ctx, "tenant2", "https://example.com/webhook")
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("Not found - different URL", func(t *testing.T) {
		_, err := tx.GetSubscriptionByTenantAndURL(ctx, "tenant1", "https://other.com/webhook")
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("Find subscription created in transaction", func(t *testing.T) {
		newSub := createTestSubscription(t, "sub_test2", "tenant1", "https://example.com/webhook2")
		err := tx.CreateSubscription(ctx, newSub)
		require.NoError(t, err)

		retrieved, err := tx.GetSubscriptionByTenantAndURL(ctx, "tenant1", "https://example.com/webhook2")
		require.NoError(t, err)
		assert.Equal(t, newSub.ID, retrieved.ID)
	})

	require.NoError(t, tx.Rollback())
}

func TestMockRepositoryTx_DeleteSubscription(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	t.Run("Delete within transaction - commit", func(t *testing.T) {
		// Create subscription in parent repo
		sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook1")
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		// Begin transaction
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		// Delete in transaction
		err = tx.DeleteSubscription(ctx, sub.ID)
		require.NoError(t, err)

		// Verify deleted in transaction
		_, err = tx.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)

		// Verify still exists in parent repo
		_, err = repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)

		// Commit
		err = tx.Commit()
		require.NoError(t, err)

		// Verify deleted in parent repo
		_, err = repo.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("Delete within transaction - rollback", func(t *testing.T) {
		// Create subscription in parent repo
		sub := createTestSubscription(t, "sub_test2", "tenant1", "https://example.com/webhook2")
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		// Begin transaction
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		// Delete in transaction
		err = tx.DeleteSubscription(ctx, sub.ID)
		require.NoError(t, err)

		// Rollback
		err = tx.Rollback()
		require.NoError(t, err)

		// Verify still exists in parent repo
		_, err = repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
	})

	t.Run("Delete not found", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		err = tx.DeleteSubscription(ctx, "sub_nonexistent")
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("Cascade delete deliveries", func(t *testing.T) {
		// Create subscription and delivery in parent repo
		sub := createTestSubscription(t, "sub_test3", "tenant1", "https://example.com/webhook3")
		require.NoError(t, repo.CreateSubscription(ctx, sub))
		dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
		require.NoError(t, repo.CreateDelivery(ctx, dlv))

		// Begin transaction and delete subscription
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.DeleteSubscription(ctx, sub.ID)
		require.NoError(t, err)

		// Verify delivery also deleted in transaction
		_, err = tx.GetDelivery(ctx, dlv.ID)
		assert.ErrorIs(t, err, ErrDeliveryNotFound)

		// Commit
		err = tx.Commit()
		require.NoError(t, err)

		// Verify delivery deleted in parent repo
		_, err = repo.GetDelivery(ctx, dlv.ID)
		assert.ErrorIs(t, err, ErrDeliveryNotFound)
	})
}

func TestMockRepositoryTx_ListSubscriptions(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Create subscriptions in parent repo
	sub1 := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook1")
	sub2 := createTestSubscription(t, "sub_test2", "tenant1", "https://example.com/webhook2")
	sub3 := createTestSubscription(t, "sub_test3", "tenant2", "https://example.com/webhook3")
	require.NoError(t, repo.CreateSubscription(ctx, sub1))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, repo.CreateSubscription(ctx, sub2))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, repo.CreateSubscription(ctx, sub3))

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	t.Run("List all", func(t *testing.T) {
		filter := &SubscriptionFilter{Limit: 100}
		subs, err := tx.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 3)
	})

	t.Run("Filter by tenant", func(t *testing.T) {
		filter := &SubscriptionFilter{TenantID: "tenant1", Limit: 100}
		subs, err := tx.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 2)
	})

	t.Run("Filter by status", func(t *testing.T) {
		filter := &SubscriptionFilter{Status: SubscriptionStatusActive, Limit: 100}
		subs, err := tx.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 3)
	})

	t.Run("With limit and offset", func(t *testing.T) {
		filter := &SubscriptionFilter{Limit: 2, Offset: 1}
		subs, err := tx.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 2)
	})

	t.Run("See changes in transaction", func(t *testing.T) {
		// Create new subscription in transaction
		sub4 := createTestSubscription(t, "sub_test4", "tenant1", "https://example.com/webhook4")
		err := tx.CreateSubscription(ctx, sub4)
		require.NoError(t, err)

		// Should see 4 subscriptions in transaction
		filter := &SubscriptionFilter{Limit: 100}
		subs, err := tx.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 4)

		// Parent repo should still see 3
		subs, err = repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 3)
	})
}

func TestMockRepositoryTx_GetDelivery(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Setup
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	t.Run("Get existing delivery", func(t *testing.T) {
		retrieved, err := tx.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, dlv.ID, retrieved.ID)
	})

	t.Run("Get not found", func(t *testing.T) {
		_, err := tx.GetDelivery(ctx, "dlv_nonexistent")
		assert.ErrorIs(t, err, ErrDeliveryNotFound)
	})

	t.Run("Get delivery created in transaction", func(t *testing.T) {
		newDlv := createTestDelivery(t, "dlv_test2", sub.ID, sub.TenantID)
		err := tx.CreateDelivery(ctx, newDlv)
		require.NoError(t, err)

		retrieved, err := tx.GetDelivery(ctx, newDlv.ID)
		require.NoError(t, err)
		assert.Equal(t, newDlv.ID, retrieved.ID)

		// Should not exist in parent repo
		_, err = repo.GetDelivery(ctx, newDlv.ID)
		assert.ErrorIs(t, err, ErrDeliveryNotFound)
	})
}

func TestMockRepositoryTx_UpdateDelivery(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Setup
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	t.Run("Update within transaction - commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		// Update in transaction
		dlv.Status = DeliveryStatusSuccess
		err = tx.UpdateDelivery(ctx, dlv)
		require.NoError(t, err)

		// Verify updated in transaction
		retrieved, err := tx.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusSuccess, retrieved.Status)

		// Verify not updated in parent repo yet
		retrieved, err = repo.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusPending, retrieved.Status)

		// Commit
		err = tx.Commit()
		require.NoError(t, err)

		// Verify updated in parent repo
		retrieved, err = repo.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusSuccess, retrieved.Status)
	})

	t.Run("Update within transaction - rollback", func(t *testing.T) {
		// Reset delivery status
		dlv.Status = DeliveryStatusPending
		require.NoError(t, repo.UpdateDelivery(ctx, dlv))

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		// Update in transaction
		dlv.Status = DeliveryStatusFailed
		err = tx.UpdateDelivery(ctx, dlv)
		require.NoError(t, err)

		// Rollback
		err = tx.Rollback()
		require.NoError(t, err)

		// Verify not updated in parent repo
		retrieved, err := repo.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusPending, retrieved.Status)
	})

	t.Run("Update not found", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		nonExistent := createTestDelivery(t, "dlv_nonexistent", sub.ID, sub.TenantID)
		err = tx.UpdateDelivery(ctx, nonExistent)
		assert.ErrorIs(t, err, ErrDeliveryNotFound)
	})
}

func TestMockRepositoryTx_GetPendingDeliveries(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Setup
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv1 := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	dlv2 := createTestDelivery(t, "dlv_test2", sub.ID, sub.TenantID)
	dlv3 := createTestDelivery(t, "dlv_test3", sub.ID, sub.TenantID)
	dlv3.Status = DeliveryStatusSuccess

	require.NoError(t, repo.CreateDelivery(ctx, dlv1))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, repo.CreateDelivery(ctx, dlv2))
	require.NoError(t, repo.CreateDelivery(ctx, dlv3))

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	t.Run("Get pending deliveries", func(t *testing.T) {
		// Get pending deliveries
		deliveries, err := tx.GetPendingDeliveries(ctx, 10)
		require.NoError(t, err)
		assert.Len(t, deliveries, 2) // Only dlv1 and dlv2 (dlv3 is success)

		// Verify they are the expected deliveries
		ids := []string{deliveries[0].ID, deliveries[1].ID}
		assert.Contains(t, ids, dlv1.ID)
		assert.Contains(t, ids, dlv2.ID)
	})

	t.Run("See new pending deliveries created in transaction", func(t *testing.T) {
		dlv4 := createTestDelivery(t, "dlv_test4", sub.ID, sub.TenantID)
		err := tx.CreateDelivery(ctx, dlv4)
		require.NoError(t, err)

		// Should see more deliveries now
		deliveries, err := tx.GetPendingDeliveries(ctx, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(deliveries), 1)
	})
}

func TestMockRepositoryTx_GetPendingDeliveries_Locking(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Setup
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv1 := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	dlv2 := createTestDelivery(t, "dlv_test2", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv1))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, repo.CreateDelivery(ctx, dlv2))

	t.Run("Limit works", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		deliveries, err := tx.GetPendingDeliveries(ctx, 1)
		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
	})

	t.Run("Skip locked in transaction", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		// Get first delivery (locks it in transaction)
		deliveries, err := tx.GetPendingDeliveries(ctx, 1)
		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		firstID := deliveries[0].ID

		// Get again (should skip locked one)
		deliveries, err = tx.GetPendingDeliveries(ctx, 1)
		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.NotEqual(t, firstID, deliveries[0].ID)
	})

	t.Run("See new pending deliveries created in transaction", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		dlv3 := createTestDelivery(t, "dlv_test3", sub.ID, sub.TenantID)
		err = tx.CreateDelivery(ctx, dlv3)
		require.NoError(t, err)

		// Should see 3 deliveries now (dlv1, dlv2, dlv3)
		deliveries, err := tx.GetPendingDeliveries(ctx, 10)
		require.NoError(t, err)
		assert.Len(t, deliveries, 3)
	})
}

func TestMockRepositoryTx_MoveToDeadLetter(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Setup
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	t.Run("Move to dead letter - commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.MoveToDeadLetter(ctx, dlv.ID, "max retries exceeded")
		require.NoError(t, err)

		// Verify in transaction
		retrieved, err := tx.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusDeadLetter, retrieved.Status)

		// Verify not changed in parent repo yet
		retrieved, err = repo.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusPending, retrieved.Status)

		// Commit
		err = tx.Commit()
		require.NoError(t, err)

		// Verify changed in parent repo
		retrieved, err = repo.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusDeadLetter, retrieved.Status)
		assert.NotNil(t, retrieved.CompletedAt)
	})

	t.Run("Move to dead letter - rollback", func(t *testing.T) {
		// Reset delivery
		dlv.Status = DeliveryStatusPending
		dlv.CompletedAt = nil
		require.NoError(t, repo.UpdateDelivery(ctx, dlv))

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.MoveToDeadLetter(ctx, dlv.ID, "test rollback")
		require.NoError(t, err)

		// Rollback
		err = tx.Rollback()
		require.NoError(t, err)

		// Verify not changed in parent repo
		retrieved, err := repo.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusPending, retrieved.Status)
	})

	t.Run("Move not found", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		err = tx.MoveToDeadLetter(ctx, "dlv_nonexistent", "test")
		assert.ErrorIs(t, err, ErrDeliveryNotFound)
	})
}

func TestMockRepositoryTx_GetDeliveryAttempts(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Setup
	sub := createTestSubscription(t, "sub_test1", "tenant1", "https://example.com/webhook")
	require.NoError(t, repo.CreateSubscription(ctx, sub))
	dlv := createTestDelivery(t, "dlv_test1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	att1 := createTestDeliveryAttempt(t, "att_test1", dlv.ID, 1)
	att2 := createTestDeliveryAttempt(t, "att_test2", dlv.ID, 2)
	require.NoError(t, repo.CreateDeliveryAttempt(ctx, att1))
	require.NoError(t, repo.CreateDeliveryAttempt(ctx, att2))

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	t.Run("Get existing attempts", func(t *testing.T) {
		attempts, err := tx.GetDeliveryAttempts(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Len(t, attempts, 2)
		assert.Equal(t, 1, attempts[0].AttemptNumber)
		assert.Equal(t, 2, attempts[1].AttemptNumber)
	})

	t.Run("Get empty attempts", func(t *testing.T) {
		attempts, err := tx.GetDeliveryAttempts(ctx, "dlv_nonexistent")
		require.NoError(t, err)
		assert.Empty(t, attempts)
	})

	t.Run("See attempts created in transaction", func(t *testing.T) {
		att3 := createTestDeliveryAttempt(t, "att_test3", dlv.ID, 3)
		err := tx.CreateDeliveryAttempt(ctx, att3)
		require.NoError(t, err)

		// Should see 3 attempts in transaction
		attempts, err := tx.GetDeliveryAttempts(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Len(t, attempts, 3)

		// Parent repo should still see 2
		attempts, err = repo.GetDeliveryAttempts(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Len(t, attempts, 2)
	})
}

func TestMockRepositoryTx_CheckIdempotency(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	// Store key in parent repo
	expiresAt := time.Now().Add(1 * time.Hour)
	err := repo.StoreIdempotencyKey(ctx, "key1", "sub_test1", expiresAt)
	require.NoError(t, err)

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	t.Run("Check existing key", func(t *testing.T) {
		exists, err := tx.CheckIdempotency(ctx, "key1", "sub_test1")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("Check non-existent key", func(t *testing.T) {
		exists, err := tx.CheckIdempotency(ctx, "key_nonexistent", "sub_test1")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Check expired key", func(t *testing.T) {
		expiredAt := time.Now().Add(-1 * time.Hour)
		err := tx.StoreIdempotencyKey(ctx, "key_expired", "sub_test1", expiredAt)
		require.NoError(t, err)

		exists, err := tx.CheckIdempotency(ctx, "key_expired", "sub_test1")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Check key stored in transaction", func(t *testing.T) {
		expiresAt := time.Now().Add(1 * time.Hour)
		err := tx.StoreIdempotencyKey(ctx, "key_tx", "sub_test1", expiresAt)
		require.NoError(t, err)

		// Should exist in transaction
		exists, err := tx.CheckIdempotency(ctx, "key_tx", "sub_test1")
		require.NoError(t, err)
		assert.True(t, exists)

		// Should not exist in parent repo
		exists, err = repo.CheckIdempotency(ctx, "key_tx", "sub_test1")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestMockRepositoryTx_StoreIdempotencyKey(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	t.Run("Store and commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		expiresAt := time.Now().Add(1 * time.Hour)
		err = tx.StoreIdempotencyKey(ctx, "key1", "sub_test1", expiresAt)
		require.NoError(t, err)

		// Commit
		err = tx.Commit()
		require.NoError(t, err)

		// Verify in parent repo
		exists, err := repo.CheckIdempotency(ctx, "key1", "sub_test1")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("Store and rollback", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		expiresAt := time.Now().Add(1 * time.Hour)
		err = tx.StoreIdempotencyKey(ctx, "key2", "sub_test1", expiresAt)
		require.NoError(t, err)

		// Rollback
		err = tx.Rollback()
		require.NoError(t, err)

		// Verify not in parent repo
		exists, err := repo.CheckIdempotency(ctx, "key2", "sub_test1")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestMockRepositoryTx_GetCircuitBreakerState(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	endpoint := "https://example.com/webhook"

	// Store state in parent repo
	state := &CircuitBreakerState{
		Endpoint:     endpoint,
		State:        CircuitBreakerStateOpen,
		FailureCount: 5,
		SuccessCount: 0,
		LastFailure:  time.Now(),
		OpenedAt:     time.Now(),
		NextRetryAt:  time.Now().Add(1 * time.Minute),
	}
	err := repo.UpdateCircuitBreakerState(ctx, state)
	require.NoError(t, err)

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	t.Run("Get existing state", func(t *testing.T) {
		retrieved, err := tx.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateOpen, retrieved.State)
		assert.Equal(t, 5, retrieved.FailureCount)
	})

	t.Run("Get default state for new endpoint", func(t *testing.T) {
		retrieved, err := tx.GetCircuitBreakerState(ctx, "https://new.com/webhook")
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, retrieved.State)
		assert.Equal(t, 0, retrieved.FailureCount)
	})

	t.Run("See state updated in transaction", func(t *testing.T) {
		newState := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateClosed,
			FailureCount: 0,
			SuccessCount: 10,
		}
		err := tx.UpdateCircuitBreakerState(ctx, newState)
		require.NoError(t, err)

		// Should see updated state in transaction
		retrieved, err := tx.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, retrieved.State)

		// Parent repo should still see old state
		retrieved, err = repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateOpen, retrieved.State)
	})
}

func TestMockRepositoryTx_UpdateCircuitBreakerState(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	endpoint := "https://example.com/webhook"

	t.Run("Update and commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		state := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateOpen,
			FailureCount: 5,
			SuccessCount: 0,
		}
		err = tx.UpdateCircuitBreakerState(ctx, state)
		require.NoError(t, err)

		// Commit
		err = tx.Commit()
		require.NoError(t, err)

		// Verify in parent repo
		retrieved, err := repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateOpen, retrieved.State)
		assert.Equal(t, 5, retrieved.FailureCount)
	})

	t.Run("Update and rollback", func(t *testing.T) {
		// Reset state in parent repo
		state := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateClosed,
			FailureCount: 0,
			SuccessCount: 0,
		}
		err := repo.UpdateCircuitBreakerState(ctx, state)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		newState := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateOpen,
			FailureCount: 10,
			SuccessCount: 0,
		}
		err = tx.UpdateCircuitBreakerState(ctx, newState)
		require.NoError(t, err)

		// Rollback
		err = tx.Rollback()
		require.NoError(t, err)

		// Verify not changed in parent repo
		retrieved, err := repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, retrieved.State)
		assert.Equal(t, 0, retrieved.FailureCount)
	})
}

func TestMockRepositoryTx_BeginTx(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	t.Run("Nested transactions not supported", func(t *testing.T) {
		_, err := tx.BeginTx(ctx)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "nested transactions are not supported")
	})
}

func TestMockRepositoryTx_Ping(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	err = tx.Ping(ctx)
	assert.NoError(t, err)
}

func TestMockRepositoryTx_Close(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	err = tx.Close()
	assert.NoError(t, err)
}

func TestMockRepositoryTx_DoubleCommit(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// First commit
	err = tx.Commit()
	require.NoError(t, err)

	// Second commit should be no-op
	err = tx.Commit()
	assert.NoError(t, err)
}

func TestMockRepositoryTx_DoubleRollback(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// First rollback
	err = tx.Rollback()
	require.NoError(t, err)

	// Second rollback should be no-op
	err = tx.Rollback()
	assert.NoError(t, err)
}

func TestMockRepositoryTx_CommitAfterRollback(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// Rollback first
	err = tx.Rollback()
	require.NoError(t, err)

	// Try to commit after rollback
	err = tx.Commit()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already rolled back")
}

func TestMockRepositoryTx_RollbackAfterCommit(t *testing.T) {
	repo := NewMockRepository()
	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// Commit first
	err = tx.Commit()
	require.NoError(t, err)

	// Rollback after commit should be no-op
	err = tx.Rollback()
	assert.NoError(t, err)
}
