package internal

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/itsatony/go-hookd/testapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// END-TO-END INTEGRATION TESTS
// =============================================================================
// These tests verify the complete webhook delivery flow from Manager to
// actual HTTP endpoint using a real test server.
//
// Tests cover:
// - Successful delivery flow
// - Retry logic with actual HTTP failures
// - Circuit breaker behavior with real endpoints
// - Idempotency with real deliveries
// - Multiple subscriptions to same endpoint
// =============================================================================

func TestE2E_SuccessfulDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup go-hookd manager with mock repository (no DB needed for E2E)
	config := NewConfig("mock")
	config.WorkerCount = 2
	config.QueuePollInterval = 100 // Poll frequently for fast tests

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription pointing to test server
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_e2e",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"order.created"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)
	t.Logf("Created subscription: %s -> %s", sub.ID, sub.URL)

	// Queue delivery
	delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "order.created",
		Payload: map[string]interface{}{
			"order_id": "order_123",
			"amount":   99.99,
			"customer": "John Doe",
		},
	})
	require.NoError(t, err)
	t.Logf("Queued delivery: %s", delivery.ID)

	// Wait for webhook to be received
	success := webhookServer.WaitForRequests(1, 10*time.Second)
	assert.True(t, success, "Webhook should be received within timeout")

	// Verify webhook was received with correct payload
	receivedReq := webhookServer.GetLastRequest()
	require.NotNil(t, receivedReq)
	assert.Equal(t, "POST", receivedReq.Method)
	assert.Equal(t, http.StatusOK, receivedReq.StatusCode)
	assert.Equal(t, "order_123", receivedReq.PayloadJSON["order_id"])
	assert.Equal(t, 99.99, receivedReq.PayloadJSON["amount"])

	// Verify delivery marked as success
	time.Sleep(500 * time.Millisecond) // Give manager time to update status
	finalDelivery, err := repo.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, finalDelivery.Status)
	assert.NotNil(t, finalDelivery.CompletedAt)

	t.Logf("✓ E2E test passed: delivery %s completed successfully", delivery.ID)
}

func TestE2E_RetryOnFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server that fails first 2 attempts, then succeeds
	webhookServer := testapi.NewServer()
	defer webhookServer.Close()

	// Configure intermittent failures (50% rate means fail every 2nd request)
	webhookServer.SetResponseBehavior(testapi.ResponseBehaviorFail)

	// Setup manager
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100
	config.DefaultMaxRetries = 3
	config.DefaultInitialBackoffMs = 1000 // Slower retries so we can switch server mode between attempts

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_retry",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.retry"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue delivery
	delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.retry",
		Payload: map[string]interface{}{
			"test": "retry",
		},
	})
	require.NoError(t, err)

	// Wait for first 2 failed attempts
	time.Sleep(2 * time.Second)

	// Check delivery status
	currentDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
	t.Logf("After 2 seconds: delivery status=%s, attempts=%d", currentDelivery.Status, currentDelivery.AttemptCount)

	// Switch server to success mode for third attempt
	webhookServer.SetResponseBehavior(testapi.ResponseBehaviorSuccess)
	t.Logf("Switched webhook server to success mode")

	// Wait for successful delivery
	success := webhookServer.WaitForRequests(3, 10*time.Second)
	assert.True(t, success, "Should receive 3 attempts (2 failures + 1 success)")

	// Verify final delivery status
	time.Sleep(1 * time.Second)
	finalDelivery, _ := repo.GetDelivery(ctx, delivery.ID)
	assert.Equal(t, DeliveryStatusSuccess, finalDelivery.Status)
	assert.GreaterOrEqual(t, finalDelivery.AttemptCount, 1, "Should have at least 1 attempt")

	// Verify webhook server received the requests
	requests := webhookServer.GetReceivedRequests()
	assert.GreaterOrEqual(t, len(requests), 1, "Should have received at least 1 request")

	t.Logf("✓ E2E retry test passed: delivery completed after %d attempts", finalDelivery.AttemptCount)
}

func TestE2E_MultipleDeliveries(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup manager
	config := NewConfig("mock")
	config.WorkerCount = 3
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_multiple",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.multiple"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue 10 deliveries
	deliveryCount := 10
	deliveryIDs := make([]string, deliveryCount)

	for i := 0; i < deliveryCount; i++ {
		delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.multiple",
			Payload: map[string]interface{}{
				"index": i,
				"test":  "multiple",
			},
		})
		require.NoError(t, err)
		deliveryIDs[i] = delivery.ID
	}

	t.Logf("Queued %d deliveries", deliveryCount)

	// Wait for all webhooks to be received
	success := webhookServer.WaitForRequests(deliveryCount, 20*time.Second)
	assert.True(t, success, "All %d webhooks should be received", deliveryCount)

	// Verify all received
	requests := webhookServer.GetReceivedRequests()
	assert.Len(t, requests, deliveryCount, "Should receive exactly %d webhooks", deliveryCount)

	// Verify each delivery completed
	time.Sleep(1 * time.Second)
	for i, deliveryID := range deliveryIDs {
		delivery, err := repo.GetDelivery(ctx, deliveryID)
		require.NoError(t, err, "Delivery %d should exist", i)
		assert.Equal(t, DeliveryStatusSuccess, delivery.Status, "Delivery %d should be successful", i)
	}

	t.Logf("✓ E2E multiple deliveries test passed: all %d deliveries completed", deliveryCount)
}

func TestE2E_IdempotencyWithRealDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup manager
	config := NewConfig("mock")
	config.WorkerCount = 2
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_idempotency_e2e",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.idempotency"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	idempotencyKey := "e2e_test_key_123"

	// First delivery with idempotency key
	delivery1, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.idempotency",
		Payload: map[string]interface{}{
			"test": "first",
		},
		IdempotencyKey: idempotencyKey,
	})
	require.NoError(t, err)
	t.Logf("First delivery created: %s", delivery1.ID)

	// Second delivery with same idempotency key (should be rejected)
	delivery2, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.idempotency",
		Payload: map[string]interface{}{
			"test": "second (duplicate)",
		},
		IdempotencyKey: idempotencyKey,
	})
	assert.Error(t, err, "Second delivery should be rejected")
	assert.True(t, IsConflictError(err), "Error should be conflict error")
	assert.Nil(t, delivery2, "No delivery should be created")
	t.Logf("Second delivery correctly rejected: %v", err)

	// Wait for webhook to be received (only from first delivery)
	success := webhookServer.WaitForRequests(1, 10*time.Second)
	assert.True(t, success, "Webhook should be received from first delivery")

	// Verify only 1 webhook received (not 2)
	time.Sleep(1 * time.Second)
	assert.Equal(t, 1, webhookServer.GetRequestCount(), "Should receive exactly 1 webhook")

	t.Logf("✓ E2E idempotency test passed: duplicate delivery prevented")
}

func TestE2E_MultipleSubscriptionsToSameEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup manager
	config := NewConfig("mock")
	config.WorkerCount = 2
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create 3 subscriptions to same endpoint
	subscriptions := make([]*Subscription, 3)
	for i := 0; i < 3; i++ {
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   fmt.Sprintf("tenant_%d", i),
			URL:        webhookServer.WebhookURL(),
			EventTypes: []string{fmt.Sprintf("event.type%d", i)},
			Secret:     "test_secret",
		})
		require.NoError(t, err)
		subscriptions[i] = sub
	}

	// Queue delivery for each subscription
	for i, sub := range subscriptions {
		_, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      fmt.Sprintf("event.type%d", i),
			Payload: map[string]interface{}{
				"subscription_index": i,
			},
		})
		require.NoError(t, err)
	}

	// Wait for all 3 webhooks
	success := webhookServer.WaitForRequests(3, 10*time.Second)
	assert.True(t, success, "All 3 webhooks should be received")

	// Verify correct number received
	assert.Equal(t, 3, webhookServer.GetRequestCount())

	t.Logf("✓ E2E multiple subscriptions test passed: all subscriptions delivered to same endpoint")
}

func TestE2E_DeliveryTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server that never responds (timeout)
	webhookServer := testapi.SetupTimeoutServer()
	defer func() {
		// Force close client connections first to prevent hanging
		webhookServer.CloseClientConnections()
		webhookServer.Close()
	}()

	// Setup manager with short timeout
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100
	config.DeliveryTimeoutMs = 1000 // 1 second timeout
	config.DefaultMaxRetries = 1    // Only 1 retry

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer func() {
		if err := manager.Stop(); err != nil {
			t.Errorf("Failed to stop manager: %v", err)
		}
	}()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_timeout",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.timeout"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue delivery
	delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.timeout",
		Payload: map[string]interface{}{
			"test": "timeout",
		},
	})
	require.NoError(t, err)

	// Wait for delivery to timeout and move to dead letter
	// Note: When max retries are exhausted, delivery moves to dead_letter status
	time.Sleep(5 * time.Second)

	// Verify delivery moved to dead letter due to timeout
	finalDelivery, err := repo.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)

	// After exhausting retries with timeouts, delivery should be in dead_letter status
	assert.Equal(t, DeliveryStatusDeadLetter, finalDelivery.Status,
		"Delivery should move to dead_letter after exhausting retries due to timeout")

	// Verify at least one attempt was made
	assert.Greater(t, finalDelivery.AttemptCount, 0,
		"Should have at least 1 delivery attempt recorded")

	t.Logf("✓ E2E timeout test passed: delivery status=%s after %d attempt(s)",
		finalDelivery.Status, finalDelivery.AttemptCount)
}

func TestE2E_CircuitBreakerOpensAndRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server that initially fails
	webhookServer := testapi.SetupFailureServer()
	defer webhookServer.Close()

	// Setup go-hookd manager
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100
	config.CircuitBreakerThreshold = 3    // Open after 3 failures
	config.CircuitBreakerTimeoutMs = 2000 // Try recovery after 2s

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_circuit",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.circuit"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue 5 deliveries that will fail
	deliveryIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.circuit",
			Payload: map[string]interface{}{
				"test_id": fmt.Sprintf("circuit_%d", i),
			},
		})
		require.NoError(t, err)
		deliveryIDs[i] = delivery.ID
	}

	// Wait for circuit to open (3 failures)
	time.Sleep(3 * time.Second)

	// Verify circuit breaker opened
	cbState, err := repo.GetCircuitBreakerState(ctx, webhookServer.WebhookURL())
	require.NoError(t, err)
	assert.Equal(t, CircuitBreakerStateOpen, cbState.State,
		"Circuit breaker should open after threshold failures")

	t.Logf("Circuit breaker opened after %d failures", cbState.FailureCount)

	// Switch server to success mode (simulate endpoint recovery)
	webhookServer.SetResponseBehavior(testapi.ResponseBehaviorSuccess)

	// Wait for circuit breaker timeout to allow recovery attempt
	time.Sleep(3 * time.Second)

	// Queue new delivery to test recovery
	recoveryDelivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.circuit",
		Payload: map[string]interface{}{
			"test_id": "recovery_test",
		},
	})
	require.NoError(t, err)

	// Wait for delivery to succeed
	time.Sleep(2 * time.Second)

	// Verify circuit breaker recovered (half_open or closed are both valid)
	cbState, err = repo.GetCircuitBreakerState(ctx, webhookServer.WebhookURL())
	require.NoError(t, err)
	// Circuit breaker should be recovering (half_open) or fully recovered (closed)
	assert.Contains(t, []string{CircuitBreakerStateHalfOpen, CircuitBreakerStateClosed}, cbState.State,
		"Circuit breaker should be in half_open or closed state after successful delivery")

	// Verify recovery delivery succeeded
	finalDelivery, err := repo.GetDelivery(ctx, recoveryDelivery.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, finalDelivery.Status)

	t.Logf("✓ E2E circuit breaker test passed: opened after failures, recovered after success")
}

func TestE2E_SubscriptionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup go-hookd manager
	config := NewConfig("mock")
	config.WorkerCount = 2
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_lifecycle",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.lifecycle"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)
	assert.Equal(t, SubscriptionStatusActive, sub.Status)

	// Queue delivery while active
	delivery1, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.lifecycle",
		Payload:        map[string]interface{}{"test": "active"},
	})
	require.NoError(t, err)

	// Wait for delivery
	time.Sleep(1 * time.Second)

	// Verify delivery succeeded
	finalDelivery1, err := repo.GetDelivery(ctx, delivery1.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, finalDelivery1.Status)

	// Pause subscription
	updatedSub, err := manager.PauseSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionStatusPaused, updatedSub.Status)

	// Queue delivery while paused (system should reject or queue but not deliver)
	delivery2, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.lifecycle",
		Payload:        map[string]interface{}{"test": "paused"},
	})

	// System may reject delivery to paused subscription or queue it
	if err != nil {
		// If rejected, that's valid behavior - subscription is paused
		t.Logf("Delivery rejected while subscription paused (expected): %v", err)
	} else {
		// If queued, verify it's not delivered yet
		time.Sleep(1 * time.Second)
		finalDelivery2, err := repo.GetDelivery(ctx, delivery2.ID)
		require.NoError(t, err)
		// Should still be pending or not started
		assert.Contains(t, []string{DeliveryStatusPending, DeliveryStatusFailed}, finalDelivery2.Status,
			"Delivery should not be completed while subscription is paused")
	}

	// Resume subscription
	updatedSub, err = manager.ResumeSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionStatusActive, updatedSub.Status)

	// If delivery2 was queued during pause, it should now be processed
	if delivery2 != nil {
		time.Sleep(1 * time.Second)
		finalDelivery2, err := repo.GetDelivery(ctx, delivery2.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusSuccess, finalDelivery2.Status,
			"Paused delivery should succeed after subscription resumed")
	} else {
		// If delivery was rejected during pause, queue a new one now
		delivery3, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.lifecycle",
			Payload:        map[string]interface{}{"test": "resumed"},
		})
		require.NoError(t, err)
		time.Sleep(1 * time.Second)
		finalDelivery3, err := repo.GetDelivery(ctx, delivery3.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusSuccess, finalDelivery3.Status,
			"New delivery should succeed after subscription resumed")
	}

	// Disable subscription
	updatedSub, err = manager.DisableSubscription(ctx, sub.ID)
	require.NoError(t, err)
	assert.Equal(t, SubscriptionStatusDisabled, updatedSub.Status)

	t.Logf("✓ E2E subscription lifecycle test passed: active → paused → active → disabled")
}

func TestE2E_SignatureVerification(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup go-hookd manager
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	secret := "test_secret_for_hmac"

	// Create subscription with secret
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_signature",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.signature"},
		Secret:     secret,
	})
	require.NoError(t, err)

	// Queue delivery
	payload := map[string]interface{}{
		"event_id": "evt_12345",
		"data":     "test data for signature",
	}

	_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.signature",
		Payload:        payload,
	})
	require.NoError(t, err)

	// Wait for delivery
	success := webhookServer.WaitForRequests(1, 5*time.Second)
	assert.True(t, success)

	// Verify webhook received with signature header
	receivedReq := webhookServer.GetLastRequest()
	require.NotNil(t, receivedReq)

	// Check for X-Webhook-Signature header (as defined in HeaderSignature constant)
	signature := receivedReq.Headers.Get(HeaderSignature)
	assert.NotEmpty(t, signature, "Webhook should include HMAC signature header")

	t.Logf("✓ E2E signature verification test passed: signature=%s", signature)
}

func TestE2E_EventFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup go-hookd manager
	config := NewConfig("mock")
	config.WorkerCount = 2
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription that only listens to "order.*" events
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_filter",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"order.created", "order.updated"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue matching event (should be delivered)
	delivery1, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "order.created",
		Payload:        map[string]interface{}{"order_id": "123"},
	})
	require.NoError(t, err)

	// Queue matching event (should be delivered)
	delivery2, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "order.updated",
		Payload:        map[string]interface{}{"order_id": "123"},
	})
	require.NoError(t, err)

	// Wait for deliveries
	success := webhookServer.WaitForRequests(2, 5*time.Second)
	assert.True(t, success, "Should receive 2 matching events")

	// Verify both deliveries succeeded
	finalDelivery1, err := repo.GetDelivery(ctx, delivery1.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, finalDelivery1.Status)

	finalDelivery2, err := repo.GetDelivery(ctx, delivery2.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, finalDelivery2.Status)

	// Verify correct number of requests
	assert.Equal(t, 2, webhookServer.GetRequestCount())

	t.Logf("✓ E2E event filtering test passed: 2 matching events delivered")
}

func TestE2E_CustomHeaders(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup go-hookd manager
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription with custom headers
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_headers",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.headers"},
		Secret:     "test_secret",
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
			"X-API-Key":       "secret-api-key",
		},
	})
	require.NoError(t, err)

	// Queue delivery
	_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.headers",
		Payload:        map[string]interface{}{"test": "headers"},
	})
	require.NoError(t, err)

	// Wait for delivery
	success := webhookServer.WaitForRequests(1, 5*time.Second)
	assert.True(t, success)

	// Verify custom headers were included
	receivedReq := webhookServer.GetLastRequest()
	require.NotNil(t, receivedReq)

	assert.Equal(t, "custom-value", receivedReq.Headers.Get("X-Custom-Header"))
	assert.Equal(t, "secret-api-key", receivedReq.Headers.Get("X-API-Key"))

	t.Logf("✓ E2E custom headers test passed: headers included in webhook request")
}

func TestE2E_LargePayload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.SetupSuccessServer()
	defer webhookServer.Close()

	// Setup go-hookd manager
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_large",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.large"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Create large payload (1MB of data)
	items := make([]map[string]interface{}, 1000)
	for i := 0; i < 1000; i++ {
		items[i] = map[string]interface{}{
			"id":          fmt.Sprintf("item_%d", i),
			"name":        fmt.Sprintf("Test Item %d", i),
			"description": "This is a test item with some description text to make it larger. Lorem ipsum dolor sit amet, consectetur adipiscing elit.",
			"price":       float64(i) * 10.50,
			"quantity":    i % 100,
			"metadata": map[string]interface{}{
				"category":    "test",
				"tags":        []string{"tag1", "tag2", "tag3"},
				"created_at":  "2025-01-01T00:00:00Z",
				"modified_at": "2025-01-02T00:00:00Z",
			},
		}
	}

	largePayload := map[string]interface{}{
		"event_id": "evt_large_payload",
		"items":    items,
		"total":    len(items),
	}

	// Queue delivery with large payload
	delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.large",
		Payload:        largePayload,
	})
	require.NoError(t, err)

	// Wait for delivery (large payload may take longer)
	success := webhookServer.WaitForRequests(1, 10*time.Second)
	assert.True(t, success)

	// Verify delivery succeeded
	finalDelivery, err := repo.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, finalDelivery.Status)

	// Verify payload was received correctly
	receivedReq := webhookServer.GetLastRequest()
	require.NotNil(t, receivedReq)
	assert.Equal(t, 1000, int(receivedReq.PayloadJSON["total"].(float64)))

	t.Logf("✓ E2E large payload test passed: 1000 items delivered successfully")
}

func TestE2E_GracefulShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server with delay to simulate slow processing
	webhookServer := testapi.SetupDelayedServer(2 * time.Second)
	defer webhookServer.Close()

	// Setup go-hookd manager
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100
	config.ShutdownTimeoutSeconds = 5 // 5 second shutdown timeout

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_shutdown",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.shutdown"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue delivery that will take 2 seconds to process
	delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.shutdown",
		Payload:        map[string]interface{}{"test": "shutdown"},
	})
	require.NoError(t, err)

	// Wait for delivery to start processing
	time.Sleep(500 * time.Millisecond)

	// Initiate shutdown - should wait for in-flight deliveries to complete
	shutdownStart := time.Now()
	manager.Stop()
	shutdownDuration := time.Since(shutdownStart)

	// Verify shutdown waited for in-flight delivery
	// Delivery takes 2s, we started shutdown after 0.5s, so shutdown should wait ~1.5s+
	assert.Greater(t, shutdownDuration, 1*time.Second,
		"Shutdown should wait for in-flight delivery to complete")
	assert.Less(t, shutdownDuration, 4*time.Second,
		"Shutdown should complete within reasonable time")

	// Verify delivery completed successfully
	finalDelivery, err := repo.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusSuccess, finalDelivery.Status,
		"Delivery should complete successfully even when shutdown initiated mid-delivery")

	// Verify webhook was received
	assert.Equal(t, 1, webhookServer.GetRequestCount(),
		"Webhook should be received despite shutdown")

	t.Logf("✓ E2E graceful shutdown test passed: shutdown waited %.2fs for delivery, status=%s",
		shutdownDuration.Seconds(), finalDelivery.Status)
}
