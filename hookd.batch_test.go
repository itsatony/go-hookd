package hookd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// QUEUE DELIVERIES BATCH TESTS
// =============================================================================

func TestManager_QueueDeliveries(t *testing.T) {
	repo := NewMockRepository()
	config := NewConfig("postgres://localhost/hookd")
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create test subscriptions
	sub1, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant-123",
		URL:        "https://example.com/webhook1",
		EventTypes: []string{"order.created"},
		Secret:     "secret-key-12345",
	})
	require.NoError(t, err)

	sub2, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant-123",
		URL:        "https://example.com/webhook2",
		EventTypes: []string{"order.updated"},
		Secret:     "secret-key-67890",
	})
	require.NoError(t, err)

	t.Run("successful batch delivery", func(t *testing.T) {
		req := &QueueDeliveriesRequest{
			Deliveries: []*QueueDeliveryRequest{
				{
					SubscriptionID: sub1.ID,
					EventType:      "order.created",
					Payload:        map[string]interface{}{"order_id": "123"},
				},
				{
					SubscriptionID: sub2.ID,
					EventType:      "order.updated",
					Payload:        map[string]interface{}{"order_id": "456"},
				},
			},
		}

		resp, err := manager.QueueDeliveries(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, 2, resp.TotalRequested)
		assert.Equal(t, 2, resp.TotalSucceeded)
		assert.Equal(t, 0, resp.TotalFailed)
		assert.Len(t, resp.Results, 2)

		// Verify all succeeded
		for i, result := range resp.Results {
			assert.True(t, result.Success, "Result %d should succeed", i)
			assert.NotNil(t, result.Result)
			assert.Nil(t, result.Error)
			assert.Equal(t, i, result.Index)
		}
	})

	t.Run("partial failure", func(t *testing.T) {
		req := &QueueDeliveriesRequest{
			Deliveries: []*QueueDeliveryRequest{
				{
					SubscriptionID: sub1.ID,
					EventType:      "order.created",
					Payload:        map[string]interface{}{"order_id": "123"},
				},
				{
					SubscriptionID: "invalid-sub-id",
					EventType:      "order.updated",
					Payload:        map[string]interface{}{"order_id": "456"},
				},
				{
					SubscriptionID: sub2.ID,
					EventType:      "order.updated",
					Payload:        map[string]interface{}{"order_id": "789"},
				},
			},
		}

		resp, err := manager.QueueDeliveries(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, 3, resp.TotalRequested)
		assert.Equal(t, 2, resp.TotalSucceeded)
		assert.Equal(t, 1, resp.TotalFailed)

		// First should succeed
		assert.True(t, resp.Results[0].Success)
		assert.NotNil(t, resp.Results[0].Result)

		// Second should fail (invalid subscription)
		assert.False(t, resp.Results[1].Success)
		assert.Nil(t, resp.Results[1].Result)
		assert.NotNil(t, resp.Results[1].Error)

		// Third should succeed
		assert.True(t, resp.Results[2].Success)
		assert.NotNil(t, resp.Results[2].Result)
	})

	t.Run("nil request", func(t *testing.T) {
		_, err := manager.QueueDeliveries(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "request is required")
	})

	t.Run("empty deliveries", func(t *testing.T) {
		req := &QueueDeliveriesRequest{
			Deliveries: []*QueueDeliveryRequest{},
		}
		_, err := manager.QueueDeliveries(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one delivery is required")
	})

	t.Run("exceeds batch size limit", func(t *testing.T) {
		deliveries := make([]*QueueDeliveryRequest, MaxBatchSize+1)
		for i := range deliveries {
			deliveries[i] = &QueueDeliveryRequest{
				SubscriptionID: sub1.ID,
				EventType:      "order.created",
				Payload:        map[string]interface{}{"order_id": i},
			}
		}

		req := &QueueDeliveriesRequest{Deliveries: deliveries}
		_, err := manager.QueueDeliveries(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "batch size exceeds maximum")
	})

	t.Run("idempotency in batch", func(t *testing.T) {
		idempotencyKey := "batch-idem-key-123"
		req := &QueueDeliveriesRequest{
			Deliveries: []*QueueDeliveryRequest{
				{
					SubscriptionID: sub1.ID,
					EventType:      "order.created",
					Payload:        map[string]interface{}{"order_id": "999"},
					IdempotencyKey: idempotencyKey,
				},
				{
					SubscriptionID: sub1.ID,
					EventType:      "order.created",
					Payload:        map[string]interface{}{"order_id": "999"},
					IdempotencyKey: idempotencyKey, // Duplicate
				},
			},
		}

		resp, err := manager.QueueDeliveries(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, 1, resp.TotalSucceeded)
		assert.Equal(t, 1, resp.TotalFailed)

		// First should succeed
		assert.True(t, resp.Results[0].Success)

		// Second should fail (duplicate idempotency key)
		assert.False(t, resp.Results[1].Success)
		assert.Contains(t, resp.Results[1].Error.Error(), "idempotency")
	})
}

// =============================================================================
// CREATE SUBSCRIPTIONS BATCH TESTS
// =============================================================================

func TestManager_CreateSubscriptions(t *testing.T) {
	repo := NewMockRepository()
	config := NewConfig("postgres://localhost/hookd")
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	t.Run("successful batch creation", func(t *testing.T) {
		req := &CreateSubscriptionsRequest{
			Subscriptions: []*CreateSubscriptionRequest{
				{
					TenantID:   "tenant-123",
					URL:        "https://example.com/webhook1",
					EventTypes: []string{"order.created"},
					Secret:     "secret-key-11111",
				},
				{
					TenantID:   "tenant-123",
					URL:        "https://example.com/webhook2",
					EventTypes: []string{"order.updated"},
					Secret:     "secret-key-22222",
				},
				{
					TenantID:   "tenant-456",
					URL:        "https://example.com/webhook3",
					EventTypes: []string{"user.registered"},
					Secret:     "secret-key-33333",
				},
			},
		}

		resp, err := manager.CreateSubscriptions(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, 3, resp.TotalRequested)
		assert.Equal(t, 3, resp.TotalSucceeded)
		assert.Equal(t, 0, resp.TotalFailed)
		assert.Len(t, resp.Results, 3)

		// Verify all succeeded
		for i, result := range resp.Results {
			assert.True(t, result.Success, "Result %d should succeed", i)
			assert.NotNil(t, result.Result)
			assert.Nil(t, result.Error)
			assert.Equal(t, i, result.Index)
			assert.NotEmpty(t, result.Result.ID)
		}
	})

	t.Run("partial failure - validation errors", func(t *testing.T) {
		req := &CreateSubscriptionsRequest{
			Subscriptions: []*CreateSubscriptionRequest{
				{
					TenantID:   "tenant-789",
					URL:        "https://example.com/webhook-valid",
					EventTypes: []string{"order.created"},
					Secret:     "secret-key-44444",
				},
				{
					TenantID:   "", // Invalid - empty tenant
					URL:        "https://example.com/webhook-invalid",
					EventTypes: []string{"order.updated"},
					Secret:     "secret-key-55555",
				},
				{
					TenantID:   "tenant-789",
					URL:        "invalid-url", // Invalid URL
					EventTypes: []string{"user.registered"},
					Secret:     "secret-key-66666",
				},
			},
		}

		resp, err := manager.CreateSubscriptions(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, 3, resp.TotalRequested)
		assert.Equal(t, 1, resp.TotalSucceeded)
		assert.Equal(t, 2, resp.TotalFailed)

		// First should succeed
		assert.True(t, resp.Results[0].Success)
		assert.NotNil(t, resp.Results[0].Result)

		// Second should fail (empty tenant)
		assert.False(t, resp.Results[1].Success)
		assert.Nil(t, resp.Results[1].Result)
		assert.NotNil(t, resp.Results[1].Error)

		// Third should fail (invalid URL)
		assert.False(t, resp.Results[2].Success)
		assert.Nil(t, resp.Results[2].Result)
		assert.NotNil(t, resp.Results[2].Error)
	})

	t.Run("nil request", func(t *testing.T) {
		_, err := manager.CreateSubscriptions(ctx, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "request is required")
	})

	t.Run("empty subscriptions", func(t *testing.T) {
		req := &CreateSubscriptionsRequest{
			Subscriptions: []*CreateSubscriptionRequest{},
		}
		_, err := manager.CreateSubscriptions(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one subscription is required")
	})

	t.Run("exceeds batch size limit", func(t *testing.T) {
		subscriptions := make([]*CreateSubscriptionRequest, MaxBatchSize+1)
		for i := range subscriptions {
			subscriptions[i] = &CreateSubscriptionRequest{
				TenantID:   "tenant-999",
				URL:        "https://example.com/webhook",
				EventTypes: []string{"test.event"},
				Secret:     "secret-key-test",
			}
		}

		req := &CreateSubscriptionsRequest{Subscriptions: subscriptions}
		_, err := manager.CreateSubscriptions(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "batch size exceeds maximum")
	})

	t.Run("duplicate subscriptions in batch", func(t *testing.T) {
		req := &CreateSubscriptionsRequest{
			Subscriptions: []*CreateSubscriptionRequest{
				{
					TenantID:   "tenant-dup",
					URL:        "https://example.com/duplicate",
					EventTypes: []string{"test.event"},
					Secret:     "secret-key-dup-1",
				},
				{
					TenantID:   "tenant-dup",
					URL:        "https://example.com/duplicate", // Same tenant + URL
					EventTypes: []string{"test.event"},
					Secret:     "secret-key-dup-2",
				},
			},
		}

		resp, err := manager.CreateSubscriptions(ctx, req)
		require.NoError(t, err)

		// First should succeed, second may fail if duplicate check happens
		assert.Equal(t, 1, resp.TotalSucceeded)
		assert.Equal(t, 1, resp.TotalFailed)
	})
}

// =============================================================================
// CONCURRENT BATCH OPERATIONS TESTS
// =============================================================================

func TestBatch_ConcurrentSafety(t *testing.T) {
	repo := NewMockRepository()
	config := NewConfig("postgres://localhost/hookd")
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription for deliveries
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant-concurrent",
		URL:        "https://example.com/concurrent",
		EventTypes: []string{"test.event"},
		Secret:     "secret-concurrent",
	})
	require.NoError(t, err)

	t.Run("concurrent batch deliveries", func(t *testing.T) {
		done := make(chan bool, 10)

		for i := 0; i < 10; i++ {
			go func(iteration int) {
				defer func() { done <- true }()

				req := &QueueDeliveriesRequest{
					Deliveries: []*QueueDeliveryRequest{
						{
							SubscriptionID: sub.ID,
							EventType:      "test.event",
							Payload:        map[string]interface{}{"iteration": iteration},
						},
					},
				}

				resp, err := manager.QueueDeliveries(ctx, req)
				assert.NoError(t, err)
				assert.Equal(t, 1, resp.TotalSucceeded)
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Timeout waiting for concurrent batch operations")
			}
		}
	})
}

// =============================================================================
// BATCH PERFORMANCE TESTS
// =============================================================================

func BenchmarkQueueDeliveries(b *testing.B) {
	repo := NewMockRepository()
	config := NewConfig("postgres://localhost/hookd")
	manager, _ := NewManager(config, repo)

	ctx := context.Background()
	manager.Start(ctx)
	defer manager.Stop()

	// Create subscription
	sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "bench-tenant",
		URL:        "https://example.com/bench",
		EventTypes: []string{"bench.event"},
		Secret:     "bench-secret-key",
	})

	// Create batch request
	deliveries := make([]*QueueDeliveryRequest, 50)
	for i := range deliveries {
		deliveries[i] = &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "bench.event",
			Payload:        map[string]interface{}{"index": i},
		}
	}

	req := &QueueDeliveriesRequest{Deliveries: deliveries}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manager.QueueDeliveries(ctx, req)
	}
}

func BenchmarkCreateSubscriptions(b *testing.B) {
	repo := NewMockRepository()
	config := NewConfig("postgres://localhost/hookd")
	manager, _ := NewManager(config, repo)

	ctx := context.Background()
	manager.Start(ctx)
	defer manager.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subscriptions := make([]*CreateSubscriptionRequest, 10)
		for j := range subscriptions {
			subscriptions[j] = &CreateSubscriptionRequest{
				TenantID:   "bench-tenant",
				URL:        "https://example.com/bench",
				EventTypes: []string{"bench.event"},
				Secret:     "bench-secret-key",
			}
		}

		req := &CreateSubscriptionsRequest{Subscriptions: subscriptions}
		manager.CreateSubscriptions(ctx, req)
	}
}
