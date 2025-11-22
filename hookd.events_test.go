package hookd

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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()
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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()
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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()
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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()
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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()
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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

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
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

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

// =============================================================================
// LIST DELIVERIES TESTS
// =============================================================================

func TestListDeliveries(t *testing.T) {
	t.Run("success with empty filter", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create subscription and queue deliveries
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue multiple deliveries
		for i := 0; i < 3; i++ {
			manager.QueueDelivery(ctx, &QueueDeliveryRequest{
				SubscriptionID: sub.ID,
				EventType:      "user.created",
				Payload:        map[string]interface{}{"index": i},
			})
		}

		// List all deliveries for tenant
		filter := &DeliveryFilter{
			TenantID: "tenant_123",
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 3)
	})

	t.Run("success with tenant ID filter", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create subscriptions for different tenants
		sub1, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_1",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		sub2, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_2",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue deliveries for different tenants
		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub1.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"tenant": "1"},
		})

		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub2.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"tenant": "2"},
		})

		// List deliveries for tenant_1 only
		filter := &DeliveryFilter{
			TenantID: "tenant_1",
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.Equal(t, "tenant_1", deliveries[0].TenantID)
	})

	t.Run("success with subscription ID filter", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create multiple subscriptions
		sub1, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook1",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		sub2, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook2",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue deliveries for different subscriptions
		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub1.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"sub": "1"},
		})

		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub2.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"sub": "2"},
		})

		// List deliveries for sub1 only
		filter := &DeliveryFilter{
			TenantID:       "tenant_123",
			SubscriptionID: StringPtr(sub1.ID),
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.Equal(t, sub1.ID, deliveries[0].SubscriptionID)
	})

	t.Run("success with status filter", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue deliveries
		delivery1, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"status": "pending"},
		})

		delivery2, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"status": "success"},
		})

		// Simulate success for delivery2
		now := time.Now()
		delivery2.Status = DeliveryStatusSuccess
		delivery2.CompletedAt = &now
		repo.UpdateDelivery(ctx, delivery2)

		// List pending deliveries only
		filter := &DeliveryFilter{
			TenantID: "tenant_123",
			Status:   StringPtr(DeliveryStatusPending),
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.Equal(t, delivery1.ID, deliveries[0].ID)
		assert.Equal(t, DeliveryStatusPending, deliveries[0].Status)
	})

	t.Run("success with event type filter", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created", "user.deleted"},
			Secret:     "test_secret",
		})

		// Queue deliveries with different event types
		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"event": "created"},
		})

		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.deleted",
			Payload:        map[string]interface{}{"event": "deleted"},
		})

		// List user.created deliveries only
		filter := &DeliveryFilter{
			TenantID:  "tenant_123",
			EventType: StringPtr("user.created"),
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.Equal(t, "user.created", deliveries[0].EventType)
	})

	t.Run("success with pagination - limit", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue 10 deliveries
		for i := 0; i < 10; i++ {
			manager.QueueDelivery(ctx, &QueueDeliveryRequest{
				SubscriptionID: sub.ID,
				EventType:      "user.created",
				Payload:        map[string]interface{}{"index": i},
			})
		}

		// List with limit of 5
		filter := &DeliveryFilter{
			TenantID: "tenant_123",
			Limit:    5,
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 5)
	})

	t.Run("success with pagination - offset", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue 10 deliveries
		for i := 0; i < 10; i++ {
			manager.QueueDelivery(ctx, &QueueDeliveryRequest{
				SubscriptionID: sub.ID,
				EventType:      "user.created",
				Payload:        map[string]interface{}{"index": i},
			})
		}

		// List with offset of 5 and limit of 3
		filter := &DeliveryFilter{
			TenantID: "tenant_123",
			Limit:    3,
			Offset:   5,
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 3)
	})

	t.Run("success with multiple filters combined", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create multiple subscriptions
		sub1, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook1",
			EventTypes: []string{"user.created", "user.deleted"},
			Secret:     "test_secret",
		})

		sub2, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook2",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue multiple deliveries
		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub1.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "1"},
		})

		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub1.ID,
			EventType:      "user.deleted",
			Payload:        map[string]interface{}{"test": "2"},
		})

		manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub2.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"test": "3"},
		})

		// List with multiple filters: specific subscription + event type
		filter := &DeliveryFilter{
			TenantID:       "tenant_123",
			SubscriptionID: StringPtr(sub1.ID),
			EventType:      StringPtr("user.created"),
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 1)
		assert.Equal(t, sub1.ID, deliveries[0].SubscriptionID)
		assert.Equal(t, "user.created", deliveries[0].EventType)
	})

	t.Run("success with no matching results", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// List deliveries for non-existent tenant
		filter := &DeliveryFilter{
			TenantID: "tenant_nonexistent",
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.NotNil(t, deliveries)
		assert.Len(t, deliveries, 0)
	})

	t.Run("success - results ordered by created_at DESC", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Queue deliveries with small time gaps
		delivery1, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"order": "first"},
		})
		time.Sleep(10 * time.Millisecond)

		delivery2, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"order": "second"},
		})
		time.Sleep(10 * time.Millisecond)

		delivery3, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"order": "third"},
		})

		// List deliveries - should be newest first
		filter := &DeliveryFilter{
			TenantID: "tenant_123",
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, deliveries, 3)
		// Newest first (DESC order)
		assert.Equal(t, delivery3.ID, deliveries[0].ID)
		assert.Equal(t, delivery2.ID, deliveries[1].ID)
		assert.Equal(t, delivery1.ID, deliveries[2].ID)
	})

	t.Run("error with nil filter", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		deliveries, err := manager.ListDeliveries(ctx, nil)

		assert.Error(t, err)
		assert.Nil(t, deliveries)
		assert.Contains(t, err.Error(), "filter")
	})

	t.Run("error with repository failure", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Inject error in repository
		repo.injectError = NewDatabaseError("list_deliveries", assert.AnError)

		filter := &DeliveryFilter{
			TenantID: "tenant_123",
		}
		deliveries, err := manager.ListDeliveries(ctx, filter)

		assert.Error(t, err)
		assert.Nil(t, deliveries)

		// Clear error
		repo.injectError = nil
	})
}
