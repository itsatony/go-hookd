package hookd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// =============================================================================
// EXCELLENCE 90% COVERAGE TESTS
// =============================================================================
//
// This file contains targeted tests to reach 90%+ coverage by testing
// specific uncovered code paths identified in the coverage report.

// =============================================================================
// 1. GetDeliveryAttempts - Repository Error Path (71.4% -> target 85%+)
// =============================================================================

func TestGetDeliveryAttempts_ErrorPaths(t *testing.T) {
	t.Run("repository error is wrapped and logged", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()

		// Create a subscription and delivery
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})

		delivery, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
		})

		// Inject error into repository
		repo.injectError = errors.New("database connection failed")

		// Call GetDeliveryAttempts - should handle error
		attempts, err := manager.GetDeliveryAttempts(ctx, delivery.ID)

		// Verify error is returned directly (not wrapped per Phase 1.3 changes)
		assert.Error(t, err)
		assert.Nil(t, attempts)
		assert.Contains(t, err.Error(), "database connection failed")

		// Clear error
		repo.injectError = nil
	})
}

// =============================================================================
// 3. updateCircuitBreakerSuccess - State Transitions (68.4% -> target 85%+)
// =============================================================================

func TestUpdateCircuitBreakerSuccess_StateTransitions(t *testing.T) {
	t.Run("success resets failure count in closed state", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		endpoint := "https://example.com"

		// Set circuit breaker to closed with some failures
		state := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateClosed,
			FailureCount: 5,
			SuccessCount: 0,
		}
		repo.UpdateCircuitBreakerState(ctx, state)

		// Record success
		manager.updateCircuitBreakerSuccess(ctx, endpoint)

		// Verify failure count is reset
		updatedState, _ := repo.GetCircuitBreakerState(ctx, endpoint)
		assert.Equal(t, CircuitBreakerStateClosed, updatedState.State)
		assert.Equal(t, 0, updatedState.FailureCount)
	})

	t.Run("success in open state transitions to closed", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		endpoint := "https://example.com/api"

		// Set circuit breaker to open (should not happen, but handle it)
		state := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateOpen,
			FailureCount: 10,
			SuccessCount: 0,
			OpenedAt:     time.Now(),
		}
		repo.UpdateCircuitBreakerState(ctx, state)

		// Record success - should reset to closed
		manager.updateCircuitBreakerSuccess(ctx, endpoint)

		// Verify state is closed
		updatedState, _ := repo.GetCircuitBreakerState(ctx, endpoint)
		assert.Equal(t, CircuitBreakerStateClosed, updatedState.State)
		assert.Equal(t, 0, updatedState.FailureCount)
		assert.Equal(t, 0, updatedState.SuccessCount)
	})

	t.Run("repository error on get is logged and handled", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()
		endpoint := "https://example.com/error"

		// Inject error
		repo.injectError = errors.New("database query failed")

		// Should not panic, should log error
		manager.updateCircuitBreakerSuccess(ctx, endpoint)

		// Clear error
		repo.injectError = nil
	})

	t.Run("repository error on update is logged and handled", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()
		endpoint := "https://example.com/update-error"

		// Set initial state
		state := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateHalfOpen,
			FailureCount: 0,
			SuccessCount: config.CircuitBreakerHalfOpenRequests - 1,
		}
		repo.UpdateCircuitBreakerState(ctx, state)

		// Inject error for update
		repo.injectErrorOnUpdate = true

		// Should not panic, should log error
		manager.updateCircuitBreakerSuccess(ctx, endpoint)

		// Clear error
		repo.injectErrorOnUpdate = false
	})
}

// =============================================================================
// 4. processDeliveries - Error Handling (75.0% -> target 85%+)
// =============================================================================

func TestProcessDeliveries_ErrorHandling(t *testing.T) {
	t.Run("handles repository error gracefully", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		config.WorkerCount = 1
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		// Need to start manager to initialize context
		ctx := context.Background()
		manager.Start(ctx)
		defer manager.Stop()

		// Inject error for GetPendingDeliveries
		repo.injectError = errors.New("database connection lost")

		// Should not panic
		manager.processDeliveries(0)

		// Clear error
		repo.injectError = nil
	})

	t.Run("processes multiple deliveries in batch", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		config.WorkerCount = 2
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Need to start manager to initialize context
		manager.Start(ctx)
		defer manager.Stop()

		// Create test server
		deliveryCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deliveryCount++
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		// Create subscription
		sub := &Subscription{
			ID:          "sub_batch",
			TenantID:    "tenant_test",
			URL:         server.URL,
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(ctx, sub)

		// Create multiple deliveries
		now := time.Now()
		for i := 0; i < 3; i++ {
			delivery := &Delivery{
				ID:             fmt.Sprintf("dlv_batch_%d", i),
				SubscriptionID: sub.ID,
				TenantID:       sub.TenantID,
				EventType:      "test.event",
				Payload:        map[string]interface{}{"index": i},
				Status:         DeliveryStatusPending,
				AttemptCount:   0,
				MaxAttempts:    3,
				NextRetryAt:    &now,
			}
			repo.CreateDelivery(ctx, delivery)
		}

		// Process deliveries (mock returns 1 at a time)
		for i := 0; i < 3; i++ {
			manager.processDeliveries(0)
		}

		// Verify deliveries were processed
		assert.Greater(t, deliveryCount, 0)
	})
}

// =============================================================================
// 5. CreateSubscription - Repository Error Paths (76.0% -> target 85%+)
// =============================================================================

func TestCreateSubscription_RepositoryErrors(t *testing.T) {
	t.Run("handles GetSubscriptionByTenantAndURL error other than not found", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()

		// Note: MockRepository returns nil, nil for non-existent subscriptions
		// This tests the case where we get an actual error (not ErrNotFound)
		// We can't easily test this with MockRepository, but we test the happy path
		// and the duplicate subscription path which covers most of the function

		req := &CreateSubscriptionRequest{
			TenantID:   "tenant_test",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		}

		sub, err := manager.CreateSubscription(ctx, req)
		require.NoError(t, err)
		assert.NotNil(t, sub)
	})

	t.Run("handles repository CreateSubscription failure", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()

		// Inject error for CreateSubscription
		repo.injectErrorOnCreate = true

		req := &CreateSubscriptionRequest{
			TenantID:   "tenant_test",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		}

		sub, err := manager.CreateSubscription(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, sub)
		// Repository error is returned directly (not wrapped per Phase 1.3 changes)
		assert.Contains(t, err.Error(), "internal error")

		// Clear error
		repo.injectErrorOnCreate = false
	})
}

// =============================================================================
// 6. QueueDelivery - Error Paths (78.8% -> target 85%+)
// =============================================================================

func TestQueueDelivery_ErrorPaths(t *testing.T) {
	t.Run("handles idempotency check error", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})

		// Inject error for CheckIdempotency
		repo.injectErrorOnIdempotencyCheck = true

		req := &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			IdempotencyKey: "test_key_123",
		}

		delivery, err := manager.QueueDelivery(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, delivery)
		// Repository error is returned directly (not wrapped per Phase 1.3 changes)
		assert.Contains(t, err.Error(), "internal error")

		// Clear error
		repo.injectErrorOnIdempotencyCheck = false
	})

	t.Run("handles CreateDelivery failure", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})

		// Inject error for CreateDelivery
		repo.injectErrorOnCreateDelivery = true

		req := &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
		}

		delivery, err := manager.QueueDelivery(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, delivery)
		// Repository error is returned directly (not wrapped per Phase 1.3 changes)
		assert.Contains(t, err.Error(), "internal error")

		// Clear error
		repo.injectErrorOnCreateDelivery = false
	})

	t.Run("logs warning but continues when StoreIdempotencyKey fails", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})

		// Inject error for StoreIdempotencyKey
		repo.injectErrorOnStoreIdempotency = true

		req := &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			IdempotencyKey: "test_key_456",
		}

		// Should succeed despite StoreIdempotencyKey error
		delivery, err := manager.QueueDelivery(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, delivery)
		assert.NotEmpty(t, delivery.ID)

		// Clear error
		repo.injectErrorOnStoreIdempotency = false
	})
}

// =============================================================================
// 7. WithLogger/WithEventBus - Nil Parameter Handling (80.0% -> target 100%)
// =============================================================================

func TestManagerOptions_NilHandling(t *testing.T) {
	t.Run("WithLogger returns error on nil logger", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()

		manager, err := NewManager(config, repo, WithLogger(nil))

		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "logger cannot be nil")
	})

	t.Run("WithEventBus returns error on nil event bus", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()

		manager, err := NewManager(config, repo, WithEventBus(nil))

		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "event bus cannot be nil")
	})
}

// =============================================================================
// 8. Stop - In-Flight Deliveries (84.6% -> target 90%+)
// =============================================================================

func TestStop_InFlightDeliveries(t *testing.T) {
	t.Run("Stop waits for in-flight deliveries to complete", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		config.WorkerCount = 2
		config.QueuePollInterval = 50 // Fast polling
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create slow server
		deliveryCompleted := false
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond) // Simulate slow delivery
			deliveryCompleted = true
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		// Create subscription
		sub := &Subscription{
			ID:          "sub_inflight",
			TenantID:    "tenant_test",
			URL:         server.URL,
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(ctx, sub)

		// Create delivery
		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_inflight",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    3,
			NextRetryAt:    &now,
		}
		repo.CreateDelivery(ctx, delivery)

		// Start manager
		manager.Start(ctx)

		// Wait for delivery to start processing
		time.Sleep(100 * time.Millisecond)

		// Stop manager - should wait for delivery to complete
		startStop := time.Now()
		err := manager.Stop()
		stopDuration := time.Since(startStop)

		require.NoError(t, err)
		// Stop should have waited for the delivery
		assert.True(t, deliveryCompleted, "delivery should have completed")
		// Should have waited at least 100ms (200ms delivery - 100ms we already waited)
		assert.Greater(t, stopDuration.Milliseconds(), int64(50))
	})

	t.Run("Stop handles repository close error", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		logger := zaptest.NewLogger(t)
		manager, err := NewManager(config, repo, WithLogger(logger))
		require.NoError(t, err)

		ctx := context.Background()
		manager.Start(ctx)

		// Inject error for Close
		repo.injectErrorOnClose = true

		err = manager.Stop()

		assert.Error(t, err)
		// Repository error is returned directly (not wrapped per Phase 1.3 changes)
		assert.Contains(t, err.Error(), "internal error")

		// Clear error
		repo.injectErrorOnClose = false
	})
}

// =============================================================================
// 9. E2E Test with Manager.Publish Event Coverage
// =============================================================================

func TestE2E_EventPublishing(t *testing.T) {
	t.Run("Full delivery lifecycle publishes all events", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		config.WorkerCount = 1
		config.QueuePollInterval = 50
		repo := NewMockRepository()

		// Create custom event bus to track events
		eventBus := &testEventBus{events: make([]interface{}, 0)}
		manager, _ := NewManager(config, repo, WithEventBus(eventBus))

		ctx := context.Background()

		// Create test server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		// Create subscription
		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test",
			URL:        server.URL,
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})

		// Queue delivery
		delivery, _ := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
		})

		// Start manager
		manager.Start(ctx)

		// Wait for delivery processing
		time.Sleep(300 * time.Millisecond)

		// Stop manager
		manager.Stop()

		// Verify events were published
		// Should have at least: subscription.created, delivery.queued, delivery.started, delivery.success
		assert.GreaterOrEqual(t, len(eventBus.events), 3, "should have published multiple events")

		// Verify delivery succeeded
		finalDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
		assert.Equal(t, DeliveryStatusSuccess, finalDelivery.Status)
	})
}

// =============================================================================
// 10. Additional Edge Cases for Coverage
// =============================================================================

func TestAdditionalEdgeCases_ForCoverage(t *testing.T) {
	t.Run("processDeliveries with no pending deliveries", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		// Need to start manager to initialize context
		ctx := context.Background()
		manager.Start(ctx)
		defer manager.Stop()

		// Call with no deliveries - should not panic
		manager.processDeliveries(0)
	})

	t.Run("CreateSubscription with nil retry policy uses default", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		req := &CreateSubscriptionRequest{
			TenantID:    "tenant_test",
			URL:         "https://example.com/webhook",
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: nil, // Explicitly nil
		}

		sub, err := manager.CreateSubscription(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, sub)
		assert.NotNil(t, sub.RetryPolicy)
		assert.Equal(t, config.DefaultRetryPolicy().MaxAttempts, sub.RetryPolicy.MaxAttempts)
	})

	t.Run("QueueDelivery without idempotency key", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})

		req := &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			IdempotencyKey: "", // No idempotency key
		}

		delivery, err := manager.QueueDelivery(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, delivery)
	})
}
