package hookd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ADVANCED CIRCUIT BREAKER TESTS
// =============================================================================
// These tests verify circuit breaker state transitions that were missing.

func TestCircuitBreaker_OpenToHalfOpenTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping circuit breaker transition test in short mode")
	}

	// Create test server that always succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	// Setup manager with short circuit breaker timeout
	config := NewConfig("mock")
	config.CircuitBreakerTimeoutMs = 1000 // 1s timeout for testing (minimum allowed)
	config.CircuitBreakerThreshold = 3
	config.CircuitBreakerHalfOpenRequests = 2

	repo := NewMockRepository()
	manager, err := NewManager(config, repo, WithAllowPrivateDestinations())
	require.NoError(t, err)

	ctx := context.Background()

	// Manually set circuit breaker to OPEN with NextRetryAt in the PAST
	// This simulates a circuit that has been open and should now try half-open
	pastTime := time.Now().Add(-1 * time.Second)
	state := &CircuitBreakerState{
		Endpoint:     server.URL,
		State:        CircuitBreakerStateOpen,
		FailureCount: 5,
		SuccessCount: 0,
		OpenedAt:     time.Now().Add(-2 * time.Second),
		NextRetryAt:  pastTime, // Critical: in the past
	}
	err = repo.UpdateCircuitBreakerState(ctx, state)
	require.NoError(t, err)

	// Create subscription and delivery
	sub := &Subscription{
		ID:          "sub_cb_test",
		TenantID:    "tenant_cb",
		URL:         server.URL,
		EventTypes:  []string{"test.circuit"},
		Secret:      "test_secret",
		RetryPolicy: DefaultRetryPolicy(),
		Status:      SubscriptionStatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	now := time.Now()
	delivery := &Delivery{
		ID:             "dlv_cb_test",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.circuit",
		Payload:        map[string]any{"test": "transition"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    3,
		NextRetryAt:    &now,
		CreatedAt:      time.Now(),
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Process delivery - should transition circuit to HALF_OPEN
	manager.processDelivery(ctx, claimForTest(t, manager, delivery))

	// CRITICAL ASSERTIONS
	updatedState, err := repo.GetCircuitBreakerState(ctx, server.URL)
	require.NoError(t, err)

	assert.Equal(t, CircuitBreakerStateHalfOpen, updatedState.State,
		"Circuit should transition from OPEN to HALF_OPEN when NextRetryAt expires")

	assert.Equal(t, 1, updatedState.SuccessCount,
		"SuccessCount should be 1 after successful delivery in HALF_OPEN")

	// FailureCount is NOT reset when transitioning to HALF_OPEN - it's kept as history
	assert.Equal(t, 5, updatedState.FailureCount,
		"FailureCount should remain at previous value (historical data)")

	// Verify delivery was successful (not blocked)
	updatedDelivery, err := repo.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)

	assert.Equal(t, DeliveryStatusSuccess, updatedDelivery.Status,
		"Delivery should succeed when circuit transitions to HALF_OPEN")

	t.Logf("✓ Circuit breaker successfully transitioned OPEN → HALF_OPEN")
}

func TestCircuitBreaker_HalfOpenToClosedTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping circuit breaker transition test in short mode")
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	// Setup manager
	config := NewConfig("mock")
	config.CircuitBreakerThreshold = 3
	config.CircuitBreakerHalfOpenRequests = 2 // Need 2 successes to close

	repo := NewMockRepository()
	manager, err := NewManager(config, repo, WithAllowPrivateDestinations())
	require.NoError(t, err)

	ctx := context.Background()

	// Set circuit to HALF_OPEN
	state := &CircuitBreakerState{
		Endpoint:     server.URL,
		State:        CircuitBreakerStateHalfOpen,
		FailureCount: 0,
		SuccessCount: 1, // Already has 1 success
		OpenedAt:     time.Now().Add(-1 * time.Second),
		NextRetryAt:  time.Now(),
	}
	err = repo.UpdateCircuitBreakerState(ctx, state)
	require.NoError(t, err)

	// Create subscription
	sub := &Subscription{
		ID:          "sub_cb_half",
		TenantID:    "tenant_cb",
		URL:         server.URL,
		EventTypes:  []string{"test.circuit"},
		Secret:      "test_secret",
		RetryPolicy: DefaultRetryPolicy(),
		Status:      SubscriptionStatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery
	now := time.Now()
	delivery := &Delivery{
		ID:             "dlv_cb_half",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.circuit",
		Payload:        map[string]any{"test": "close"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    3,
		NextRetryAt:    &now,
		CreatedAt:      time.Now(),
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Process delivery - should close circuit
	manager.processDelivery(ctx, claimForTest(t, manager, delivery))

	// ASSERTIONS
	updatedState, err := repo.GetCircuitBreakerState(ctx, server.URL)
	require.NoError(t, err)

	assert.Equal(t, CircuitBreakerStateClosed, updatedState.State,
		"Circuit should transition to CLOSED after reaching success threshold (2 successes)")

	assert.Equal(t, 0, updatedState.SuccessCount,
		"SuccessCount should reset to 0 after closing")

	assert.Equal(t, 0, updatedState.FailureCount,
		"FailureCount should be 0 in CLOSED state")

	t.Logf("✓ Circuit breaker successfully transitioned HALF_OPEN → CLOSED")
}

func TestCircuitBreaker_HalfOpenToOpenReopen(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping circuit breaker reopen test in short mode")
	}

	// Create test server that always fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"server error"}`))
	}))
	defer server.Close()

	// Setup manager
	config := NewConfig("mock")
	config.CircuitBreakerThreshold = 3
	config.CircuitBreakerHalfOpenRequests = 2
	config.CircuitBreakerTimeoutMs = 1000

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()

	// Set circuit to HALF_OPEN
	state := &CircuitBreakerState{
		Endpoint:     server.URL,
		State:        CircuitBreakerStateHalfOpen,
		FailureCount: 0,
		SuccessCount: 0,
		OpenedAt:     time.Now().Add(-2 * time.Second),
		NextRetryAt:  time.Now().Add(-1 * time.Second),
	}
	err = repo.UpdateCircuitBreakerState(ctx, state)
	require.NoError(t, err)

	// Create subscription
	sub := &Subscription{
		ID:          "sub_cb_reopen",
		TenantID:    "tenant_cb",
		URL:         server.URL,
		EventTypes:  []string{"test.circuit"},
		Secret:      "test_secret",
		RetryPolicy: DefaultRetryPolicy(),
		Status:      SubscriptionStatusActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create first delivery (will fail and reopen circuit)
	now := time.Now()
	delivery1 := &Delivery{
		ID:             "dlv_cb_reopen1",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.circuit",
		Payload:        map[string]any{"test": "reopen"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    3,
		NextRetryAt:    &now,
		CreatedAt:      time.Now(),
	}
	err = repo.CreateDelivery(ctx, delivery1)
	require.NoError(t, err)

	// Process delivery - should fail and reopen circuit
	manager.processDelivery(ctx, claimForTest(t, manager, delivery1))
	// processDelivery logs errors internally, doesn't return them

	// ASSERTIONS - Circuit should be OPEN again
	updatedState, err := repo.GetCircuitBreakerState(ctx, server.URL)
	require.NoError(t, err)

	assert.Equal(t, CircuitBreakerStateOpen, updatedState.State,
		"Circuit should reopen (HALF_OPEN → OPEN) after failure")

	// Create second delivery - should be blocked by open circuit
	delivery2 := &Delivery{
		ID:             "dlv_cb_reopen2",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.circuit",
		Payload:        map[string]any{"test": "blocked"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    3,
		NextRetryAt:    &now,
		CreatedAt:      time.Now(),
	}
	err = repo.CreateDelivery(ctx, delivery2)
	require.NoError(t, err)

	// Try to process second delivery - should be blocked
	manager.processDelivery(ctx, claimForTest(t, manager, delivery2))
	// Delivery should be blocked by circuit breaker (logged internally)

	// Verify second delivery was not attempted
	updatedDelivery2, err := repo.GetDelivery(ctx, delivery2.ID)
	require.NoError(t, err)

	assert.Equal(t, 0, updatedDelivery2.AttemptCount,
		"Second delivery should not be attempted (blocked by circuit)")

	t.Logf("✓ Circuit breaker successfully reopened and blocked subsequent deliveries")
}

func TestCircuitBreaker_ConcurrentStateUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent circuit breaker test in short mode")
	}

	// Create test server that fails
	failCount := 0
	var failMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failMu.Lock()
		failCount++
		failMu.Unlock()

		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"concurrent failure"}`))
	}))
	defer server.Close()

	// Setup manager
	config := NewConfig("mock")
	config.CircuitBreakerThreshold = 5
	config.WorkerCount = 3 // Multiple workers

	repo := NewMockRepository()
	manager, err := NewManager(config, repo, WithAllowPrivateDestinations())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_cb_concurrent",
		URL:        server.URL,
		EventTypes: []string{"test.concurrent"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue 10 deliveries rapidly (all will fail)
	deliveryCount := 10
	for i := 0; i < deliveryCount; i++ {
		_, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.concurrent",
			Payload: map[string]any{
				"index": i,
			},
		})
		require.NoError(t, err)
	}

	// Wait for processing
	time.Sleep(3 * time.Second)

	// Verify circuit opened
	state, err := repo.GetCircuitBreakerState(ctx, server.URL)
	require.NoError(t, err)

	assert.Equal(t, CircuitBreakerStateOpen, state.State,
		"Circuit should be open after threshold failures")

	// Verify failure count is accurate (no lost updates from concurrent access)
	assert.GreaterOrEqual(t, state.FailureCount, config.CircuitBreakerThreshold,
		"FailureCount should be at least threshold value (no lost updates)")

	failMu.Lock()
	actualFails := failCount
	failMu.Unlock()

	t.Logf("Server received %d failures, circuit breaker recorded %d",
		actualFails, state.FailureCount)

	// Allow some tolerance for concurrent updates, but should be close
	assert.LessOrEqual(t, state.FailureCount, actualFails,
		"Circuit breaker should not count more failures than occurred")

	t.Logf("✓ Circuit breaker handled concurrent updates correctly")
}
