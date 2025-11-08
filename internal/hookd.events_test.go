package internal

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// QUEUE DELIVERY TESTS
// =============================================================================

func TestQueueDelivery(t *testing.T) {
	t.Run("success with valid request", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue delivery
		req := &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload: map[string]interface{}{
				"user_id": "123",
				"email":   "user@example.com",
			},
		}

		delivery, err := manager.QueueDelivery(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, delivery)
		assert.NotEmpty(t, delivery.ID)
		assert.Equal(t, sub.ID, delivery.SubscriptionID)
		assert.Equal(t, sub.TenantID, delivery.TenantID)
		assert.Equal(t, req.EventType, delivery.EventType)
		assert.Equal(t, req.Payload, delivery.Payload)
		assert.Equal(t, DeliveryStatusPending, delivery.Status)
		assert.Equal(t, 0, delivery.AttemptCount)
		assert.NotNil(t, delivery.NextRetryAt)
		assert.NotZero(t, delivery.CreatedAt)
	})

	t.Run("success with idempotency key", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue delivery with idempotency key
		req := &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "data"},
			IdempotencyKey: "unique_key_123",
		}

		delivery, err := manager.QueueDelivery(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, delivery)
	})

	t.Run("error with duplicate idempotency key", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue first delivery
		req := &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "data"},
			IdempotencyKey: "unique_key_456",
		}
		manager.QueueDelivery(ctx, req)

		// Try to queue duplicate
		delivery2, err2 := manager.QueueDelivery(ctx, req)

		assert.Error(t, err2)
		assert.Nil(t, delivery2)
		assert.Contains(t, err2.Error(), "idempotency")
	})

	t.Run("error with invalid request", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Queue delivery with invalid request (missing required fields)
		req := &QueueDeliveryRequest{
			SubscriptionID: "", // Missing
			EventType:      "user.created",
		}

		delivery, err := manager.QueueDelivery(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, delivery)
	})

	t.Run("error with non-existent subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		req := &QueueDeliveryRequest{
			SubscriptionID: "sub_nonexistent",
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "data"},
		}

		delivery, err := manager.QueueDelivery(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, delivery)
	})

	t.Run("error with inactive subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create and disable subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})
		manager.DisableSubscription(ctx, sub.ID)

		// Try to queue delivery
		req := &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "data"},
		}

		delivery, err := manager.QueueDelivery(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, delivery)
		assert.Contains(t, err.Error(), "not active")
	})
}

// =============================================================================
// GET DELIVERY TESTS
// =============================================================================

func TestGetDelivery(t *testing.T) {
	t.Run("success with existing delivery", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription and queue delivery
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		queued, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "data"},
		})

		// Get delivery
		delivery, err := manager.GetDelivery(ctx, queued.ID)

		require.NoError(t, err)
		assert.NotNil(t, delivery)
		assert.Equal(t, queued.ID, delivery.ID)
		assert.Equal(t, queued.SubscriptionID, delivery.SubscriptionID)
	})

	t.Run("error with empty ID", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		delivery, err := manager.GetDelivery(ctx, "")

		assert.Error(t, err)
		assert.Nil(t, delivery)
	})

	t.Run("error with non-existent delivery", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		delivery, err := manager.GetDelivery(ctx, "dlv_nonexistent")

		assert.Error(t, err)
		assert.Nil(t, delivery)
	})
}

// =============================================================================
// GET DELIVERY ATTEMPTS TESTS
// =============================================================================

func TestGetDeliveryAttempts(t *testing.T) {
	t.Run("success with existing delivery", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription and queue delivery
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		delivery, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "data"},
		})

		// Get attempts (should be empty initially)
		attempts, err := manager.GetDeliveryAttempts(ctx, delivery.ID)

		require.NoError(t, err)
		assert.NotNil(t, attempts)
		assert.Len(t, attempts, 0)
	})

	t.Run("error with empty delivery ID", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		attempts, err := manager.GetDeliveryAttempts(ctx, "")

		assert.Error(t, err)
		assert.Nil(t, attempts)
	})
}

// =============================================================================
// RETRY DELIVERY TESTS
// =============================================================================

func TestRetryDelivery(t *testing.T) {
	t.Run("success retrying failed delivery", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription and queue delivery
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		delivery, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "data"},
		})

		// Simulate failure by updating status
		delivery.Status = DeliveryStatusFailed
		repo.UpdateDelivery(ctx, delivery)

		// Retry delivery
		retried, err := manager.RetryDelivery(ctx, delivery.ID)

		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusPending, retried.Status)
		assert.NotNil(t, retried.NextRetryAt)
	})

	t.Run("error with empty delivery ID", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		delivery, err := manager.RetryDelivery(ctx, "")

		assert.Error(t, err)
		assert.Nil(t, delivery)
	})

	t.Run("error with non-existent delivery", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		delivery, err := manager.RetryDelivery(ctx, "dlv_nonexistent")

		assert.Error(t, err)
		assert.Nil(t, delivery)
	})

	t.Run("error retrying successful delivery", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription and queue delivery
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		delivery, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "data"},
		})

		// Simulate success
		now := time.Now()
		delivery.Status = DeliveryStatusSuccess
		delivery.CompletedAt = &now
		repo.UpdateDelivery(ctx, delivery)

		// Try to retry
		retried, err := manager.RetryDelivery(ctx, delivery.ID)

		assert.Error(t, err)
		assert.Nil(t, retried)
		assert.Contains(t, err.Error(), "cannot retry successful")
	})
}
