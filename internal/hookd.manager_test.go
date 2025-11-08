package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// =============================================================================
// MANAGER INITIALIZATION TESTS
// =============================================================================

func TestNewManager(t *testing.T) {
	t.Run("success with valid config", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()

		manager, err := NewManager(config, repo)

		require.NoError(t, err)
		assert.NotNil(t, manager)
		assert.Equal(t, config, manager.config)
		assert.Equal(t, repo, manager.repo)
		assert.NotNil(t, manager.logger)
		assert.NotNil(t, manager.httpClient)
		assert.NotNil(t, manager.eventBus)
		assert.False(t, manager.IsStarted())
	})

	t.Run("error with nil config", func(t *testing.T) {
		repo := NewMockRepository()

		manager, err := NewManager(nil, repo)

		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "configuration is required")
	})

	t.Run("error with nil repository", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")

		manager, err := NewManager(config, nil)

		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "repository is required")
	})

	t.Run("error with invalid config", func(t *testing.T) {
		config := NewConfig("")
		repo := NewMockRepository()

		manager, err := NewManager(config, repo)

		assert.Error(t, err)
		assert.Nil(t, manager)
	})

	t.Run("success with custom logger", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		customLogger := zap.NewNop()

		manager, err := NewManager(config, repo, WithLogger(customLogger))

		require.NoError(t, err)
		assert.Equal(t, customLogger, manager.logger)
	})

	t.Run("success with custom event bus", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		customBus := &testEventBus{events: make([]interface{}, 0)}

		manager, err := NewManager(config, repo, WithEventBus(customBus))

		require.NoError(t, err)
		assert.Equal(t, customBus, manager.eventBus)
	})

	t.Run("success with custom HTTP client", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		customClient := &http.Client{Timeout: 5 * time.Second}

		manager, err := NewManager(config, repo, WithHTTPClient(customClient))

		require.NoError(t, err)
		assert.Equal(t, customClient, manager.httpClient)
	})

	t.Run("error with nil HTTP client", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()

		manager, err := NewManager(config, repo, WithHTTPClient(nil))

		assert.Error(t, err)
		assert.Nil(t, manager)
		assert.Contains(t, err.Error(), "HTTP client cannot be nil")
	})

	t.Run("noOpEventBus used when no event bus provided", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()

		manager, err := NewManager(config, repo)

		require.NoError(t, err)
		assert.NotNil(t, manager)
		assert.NotNil(t, manager.eventBus)

		// Test noOpEventBus methods (for coverage)
		manager.eventBus.Publish("test.topic", map[string]string{"test": "data"})
		unsubscribe := manager.eventBus.Subscribe("test.topic", func(data interface{}) {
			// This handler will never be called
		})
		assert.NotNil(t, unsubscribe)
		unsubscribe()
	})
}

// =============================================================================
// MANAGER LIFECYCLE TESTS
// =============================================================================

func TestManagerLifecycle(t *testing.T) {
	t.Run("start manager successfully", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		config.WorkerCount = 2
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		err := manager.Start(ctx)

		require.NoError(t, err)
		assert.True(t, manager.IsStarted())

		// Cleanup
		manager.Stop()
	})

	t.Run("cannot start twice", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		err1 := manager.Start(ctx)
		err2 := manager.Start(ctx)

		require.NoError(t, err1)
		assert.Error(t, err2)
		assert.Contains(t, err2.Error(), "already started")

		// Cleanup
		manager.Stop()
	})

	t.Run("stop manager gracefully", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		config.WorkerCount = 2
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		manager.Start(ctx)

		// Give workers time to start
		time.Sleep(10 * time.Millisecond)

		err := manager.Stop()

		require.NoError(t, err)
	})

	t.Run("stop before start is safe", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		err := manager.Stop()

		require.NoError(t, err)
	})

	t.Run("multiple stop calls are safe", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		manager.Start(ctx)

		err1 := manager.Stop()
		err2 := manager.Stop()

		assert.NoError(t, err1)
		assert.NoError(t, err2)
	})
}

// =============================================================================
// WORKER POOL TESTS
// =============================================================================

func TestWorkerPool(t *testing.T) {
	t.Run("workers process deliveries", func(t *testing.T) {
		// Create test HTTP server
		deliveryCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			deliveryCount++
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		config := NewConfig("postgres://localhost/test")
		config.WorkerCount = 2
		config.QueuePollInterval = 100
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		// Create subscription
		sub := &Subscription{
			ID:          "sub_test",
			TenantID:    "tenant_test",
			URL:         server.URL,
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(context.Background(), sub)

		// Create delivery
		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_test",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    3,
			NextRetryAt:    &now,
		}
		repo.CreateDelivery(context.Background(), delivery)

		// Start manager
		ctx := context.Background()
		manager.Start(ctx)

		// Wait for delivery processing
		time.Sleep(500 * time.Millisecond)

		// Cleanup
		manager.Stop()

		// Verify delivery was processed
		assert.Greater(t, deliveryCount, 0, "delivery should have been processed")
	})

	t.Run("worker respects concurrency limit", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		config.WorkerCount = 3
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		manager.Start(ctx)

		// Verify worker semaphore size
		assert.Equal(t, 3, cap(manager.workerSem))

		// Cleanup
		manager.Stop()
	})
}

// =============================================================================
// DELIVERY PROCESSING TESTS
// =============================================================================

func TestDeliveryProcessing(t *testing.T) {
	t.Run("successful delivery marks status as success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub := &Subscription{
			ID:          "sub_test",
			TenantID:    "tenant_test",
			URL:         server.URL,
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(ctx, sub)

		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_test",
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

		manager.processDelivery(ctx, delivery)

		// Verify delivery status
		updatedDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
		assert.Equal(t, DeliveryStatusSuccess, updatedDelivery.Status)
		assert.NotNil(t, updatedDelivery.CompletedAt)
	})

	t.Run("failed delivery with retryable status schedules retry", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub := &Subscription{
			ID:          "sub_test",
			TenantID:    "tenant_test",
			URL:         server.URL,
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(ctx, sub)

		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_test",
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

		manager.processDelivery(ctx, delivery)

		// Verify delivery status - should be pending for retry, not failed
		updatedDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
		assert.Equal(t, DeliveryStatusPending, updatedDelivery.Status)
		assert.Equal(t, 1, updatedDelivery.AttemptCount)
		assert.NotNil(t, updatedDelivery.NextRetryAt)
	})

	t.Run("exhausted retries moves to dead letter queue", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub := &Subscription{
			ID:          "sub_test",
			TenantID:    "tenant_test",
			URL:         server.URL,
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(ctx, sub)

		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_test",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusFailed,
			AttemptCount:   3,
			MaxAttempts:    3,
			NextRetryAt:    &now,
		}
		repo.CreateDelivery(ctx, delivery)

		manager.processDelivery(ctx, delivery)

		// Verify delivery moved to DLQ
		updatedDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
		assert.Equal(t, DeliveryStatusDeadLetter, updatedDelivery.Status)
		assert.NotNil(t, updatedDelivery.CompletedAt)
	})

	t.Run("non-retryable status code does not retry", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest) // 400 is not retryable
		}))
		defer server.Close()

		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub := &Subscription{
			ID:          "sub_test",
			TenantID:    "tenant_test",
			URL:         server.URL,
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(ctx, sub)

		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_test",
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

		manager.processDelivery(ctx, delivery)

		// Verify delivery moved to DLQ immediately
		updatedDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
		assert.Equal(t, DeliveryStatusDeadLetter, updatedDelivery.Status)
	})
}

// =============================================================================
// CIRCUIT BREAKER TESTS
// =============================================================================

func TestCircuitBreaker(t *testing.T) {
	t.Run("circuit breaker opens after threshold failures", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		config := NewConfig("postgres://localhost/test")
		config.CircuitBreakerThreshold = 2
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Record failures
		for i := 0; i < 3; i++ {
			manager.updateCircuitBreakerFailure(ctx, server.URL)
		}

		// Verify circuit breaker is open
		state, _ := repo.GetCircuitBreakerState(ctx, server.URL)
		assert.Equal(t, CircuitBreakerStateOpen, state.State)
		assert.GreaterOrEqual(t, state.FailureCount, config.CircuitBreakerThreshold)
	})

	t.Run("circuit breaker closes after successful requests", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		config.CircuitBreakerHalfOpenRequests = 2
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		endpoint := "https://example.com"

		// Set circuit to half-open
		state := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateHalfOpen,
			FailureCount: 0,
			SuccessCount: 0,
		}
		repo.UpdateCircuitBreakerState(ctx, state)

		// Record successful requests
		for i := 0; i < 3; i++ {
			manager.updateCircuitBreakerSuccess(ctx, endpoint)
		}

		// Verify circuit breaker is closed
		updatedState, _ := repo.GetCircuitBreakerState(ctx, endpoint)
		assert.Equal(t, CircuitBreakerStateClosed, updatedState.State)
		assert.Equal(t, 0, updatedState.FailureCount)
	})

	t.Run("open circuit breaker blocks deliveries", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		// Set circuit breaker to open
		ctx := context.Background()
		state := &CircuitBreakerState{
			Endpoint:     server.URL,
			State:        CircuitBreakerStateOpen,
			FailureCount: 5,
			OpenedAt:     time.Now(),
			NextRetryAt:  time.Now().Add(1 * time.Hour),
		}
		repo.UpdateCircuitBreakerState(ctx, state)

		sub := &Subscription{
			ID:          "sub_test",
			TenantID:    "tenant_test",
			URL:         server.URL,
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(ctx, sub)

		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_test",
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

		manager.processDelivery(ctx, delivery)

		// Verify delivery was not successful (circuit breaker blocked it)
		updatedDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
		assert.NotEqual(t, DeliveryStatusSuccess, updatedDelivery.Status)
	})
}

// =============================================================================
// EVENT PUBLISHING TESTS
// =============================================================================

func TestEventPublishing(t *testing.T) {
	t.Run("manager publishes delivery events", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		eventBus := &testEventBus{events: make([]interface{}, 0)}
		manager, _ := NewManager(config, repo, WithEventBus(eventBus))

		sub := &Subscription{
			ID:       "sub_test",
			TenantID: "tenant_test",
			URL:      "https://example.com",
		}

		delivery := &Delivery{
			ID:             "dlv_test",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Status:         DeliveryStatusSuccess,
		}

		manager.publishDeliveryEvent(EventTopicDeliverySuccess, delivery, sub, nil)

		assert.Len(t, eventBus.events, 1)
	})

	t.Run("manager publishes circuit breaker events", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		eventBus := &testEventBus{events: make([]interface{}, 0)}
		manager, _ := NewManager(config, repo, WithEventBus(eventBus))

		state := &CircuitBreakerState{
			Endpoint: "https://example.com",
			State:    CircuitBreakerStateOpen,
		}

		manager.publishCircuitBreakerEvent(EventTopicCircuitOpened, state)

		assert.Len(t, eventBus.events, 1)
	})
}

// =============================================================================
// ERROR HANDLING TESTS
// =============================================================================

func TestErrorHandling(t *testing.T) {
	t.Run("handles missing subscription gracefully", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_test",
			SubscriptionID: "sub_nonexistent",
			TenantID:       "tenant_test",
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    3,
			NextRetryAt:    &now,
		}
		repo.CreateDelivery(ctx, delivery)

		// Should not panic
		manager.processDelivery(ctx, delivery)

		// Since subscription doesn't exist, processDelivery returns early
		// Delivery should remain in pending status (not processed)
		updatedDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
		assert.NotNil(t, updatedDelivery)
		// Delivery status should be unchanged since processing was skipped
		assert.Equal(t, DeliveryStatusPending, updatedDelivery.Status)
	})

	t.Run("handles network errors gracefully", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub := &Subscription{
			ID:          "sub_test",
			TenantID:    "tenant_test",
			URL:         "http://invalid-domain-that-does-not-exist.local",
			EventTypes:  []string{"test.event"},
			Secret:      "test_secret",
			RetryPolicy: DefaultRetryPolicy(),
			Status:      SubscriptionStatusActive,
		}
		repo.CreateSubscription(ctx, sub)

		now := time.Now()
		delivery := &Delivery{
			ID:             "dlv_test",
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

		// Should not panic
		manager.processDelivery(ctx, delivery)

		// Delivery should be pending for retry (network errors are retryable)
		updatedDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
		assert.Equal(t, DeliveryStatusPending, updatedDelivery.Status)
	})
}

// =============================================================================
// HELPER TYPES AND FUNCTIONS
// =============================================================================

// testEventBus is a simple event bus for testing
type testEventBus struct {
	events []interface{}
}

func (t *testEventBus) Publish(topic string, data interface{}) {
	t.events = append(t.events, data)
}

func (t *testEventBus) Subscribe(topic string, handler func(interface{})) func() {
	return func() {}
}
