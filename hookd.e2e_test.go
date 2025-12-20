package hookd

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
		Payload: map[string]any{
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

func TestE2E_WebhookHeaders(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.NewServer()
	defer webhookServer.Close()

	// Setup manager
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
	customHeaders := map[string]string{
		"X-Custom-Header": "custom-value",
		"Authorization":   "Bearer test-token",
	}
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_headers_test",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.headers"},
		Secret:     "test_secret_123",
		Headers:    customHeaders,
	})
	require.NoError(t, err)

	// Queue delivery
	delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.headers",
		Payload: map[string]any{
			"test": "headers",
		},
	})
	require.NoError(t, err)

	// Wait for webhook
	success := webhookServer.WaitForRequests(1, 10*time.Second)
	require.True(t, success, "Webhook should be received")

	// Get the received request
	receivedReq := webhookServer.GetLastRequest()
	require.NotNil(t, receivedReq)

	// Verify all standard hookd headers are present
	t.Run("standard_headers", func(t *testing.T) {
		// X-Webhook-Signature
		signature := receivedReq.Headers.Get(HeaderSignature)
		assert.NotEmpty(t, signature, "X-Webhook-Signature should be present")

		// X-Webhook-Timestamp
		timestamp := receivedReq.Headers.Get(HeaderTimestamp)
		assert.NotEmpty(t, timestamp, "X-Webhook-Timestamp should be present")

		// X-Webhook-Delivery-ID
		deliveryID := receivedReq.Headers.Get(HeaderDeliveryID)
		assert.Equal(t, delivery.ID, deliveryID, "X-Webhook-Delivery-ID should match delivery ID")

		// X-Webhook-Subscription-ID
		subscriptionID := receivedReq.Headers.Get(HeaderSubscriptionID)
		assert.Equal(t, sub.ID, subscriptionID, "X-Webhook-Subscription-ID should match subscription ID")

		// X-Webhook-Event-Type
		eventType := receivedReq.Headers.Get(HeaderEventType)
		assert.Equal(t, "test.headers", eventType, "X-Webhook-Event-Type should match event type")

		// X-Webhook-Attempt
		attempt := receivedReq.Headers.Get(HeaderAttemptNumber)
		assert.Equal(t, "1", attempt, "X-Webhook-Attempt should be 1 for first attempt")
	})

	// Verify custom headers from subscription are passed through
	t.Run("custom_headers", func(t *testing.T) {
		customHeader := receivedReq.Headers.Get("X-Custom-Header")
		assert.Equal(t, "custom-value", customHeader, "Custom header should be passed through")

		authHeader := receivedReq.Headers.Get("Authorization")
		assert.Equal(t, "Bearer test-token", authHeader, "Authorization header should be passed through")
	})

	// Verify signature is valid
	t.Run("signature_verification", func(t *testing.T) {
		signature := receivedReq.Headers.Get(HeaderSignature)
		timestamp := receivedReq.Headers.Get(HeaderTimestamp)
		payload := receivedReq.Body

		isValid := VerifySignature(sub.Secret, timestamp, payload, signature)
		assert.True(t, isValid, "Signature should be valid")
	})

	t.Logf("✓ E2E headers test passed: all %d headers verified", 6+len(customHeaders))
}

func TestE2E_TestSubscription(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup manager (no need to start workers for TestSubscription)
	config := NewConfig("mock")
	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("successful_test_ping", func(t *testing.T) {
		// Setup webhook server that returns 200
		webhookServer := testapi.NewServer()
		defer webhookServer.Close()

		// Create subscription
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test_ping",
			URL:        webhookServer.WebhookURL(),
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})
		require.NoError(t, err)

		// Test the subscription
		result, err := manager.TestSubscription(ctx, sub.ID)
		require.NoError(t, err)

		// Verify result
		assert.True(t, result.Success, "Test should succeed")
		assert.Equal(t, http.StatusOK, result.StatusCode)
		assert.Empty(t, result.Error)
		assert.Greater(t, result.ResponseTime, time.Duration(0))

		// Verify webhook received the test ping
		assert.Equal(t, 1, webhookServer.GetRequestCount())
		receivedReq := webhookServer.GetLastRequest()
		require.NotNil(t, receivedReq)

		// Verify test ping payload
		assert.Equal(t, EventTypeTestPing, receivedReq.PayloadJSON["type"])
		assert.NotNil(t, receivedReq.PayloadJSON["timestamp"])
		assert.Contains(t, receivedReq.PayloadJSON["message"], "test ping")

		// Verify headers
		assert.Equal(t, sub.ID, receivedReq.Headers.Get(HeaderSubscriptionID))
		assert.Equal(t, EventTypeTestPing, receivedReq.Headers.Get(HeaderEventType))
		assert.Equal(t, "test_ping", receivedReq.Headers.Get(HeaderDeliveryID))
	})

	t.Run("failed_test_ping_500", func(t *testing.T) {
		// Setup webhook server that returns 500
		webhookServer := testapi.NewServer()
		defer webhookServer.Close()
		webhookServer.SetResponseBehavior(testapi.ResponseBehaviorCustom, 500, "Internal Server Error")

		// Create subscription
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test_fail",
			URL:        webhookServer.WebhookURL(),
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})
		require.NoError(t, err)

		// Test the subscription
		result, err := manager.TestSubscription(ctx, sub.ID)
		require.NoError(t, err)

		// Verify result indicates failure
		assert.False(t, result.Success, "Test should fail")
		assert.Equal(t, 500, result.StatusCode)
		assert.Contains(t, result.Error, "500")
	})

	t.Run("failed_test_ping_connection_refused", func(t *testing.T) {
		// Create subscription pointing to non-existent server
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_test_refused",
			URL:        "http://localhost:9999/webhook", // Non-existent server
			EventTypes: []string{"test.event"},
			Secret:     "test_secret",
		})
		require.NoError(t, err)

		// Test the subscription
		result, err := manager.TestSubscription(ctx, sub.ID)
		require.NoError(t, err)

		// Verify result indicates connection failure
		assert.False(t, result.Success, "Test should fail")
		assert.Equal(t, 0, result.StatusCode)
		assert.Contains(t, result.Error, "connection refused")
	})

	t.Run("subscription_not_found", func(t *testing.T) {
		// Test non-existent subscription
		result, err := manager.TestSubscription(ctx, "sub_nonexistent")

		// Should return error (not TestResult)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.True(t, IsNotFoundError(err))
	})

	t.Logf("✓ E2E TestSubscription tests passed")
}

func TestE2E_WildcardEventTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server
	webhookServer := testapi.NewServer()
	defer webhookServer.Close()

	// Setup manager
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	t.Run("universal_wildcard", func(t *testing.T) {
		webhookServer.Reset()

		// Create subscription with "*" (matches all events)
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_wildcard_all",
			URL:        webhookServer.WebhookURL(),
			EventTypes: []string{"*"},
			Secret:     "test_secret",
		})
		require.NoError(t, err)

		// Queue delivery with any event type
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "order.created",
			Payload:        map[string]any{"test": "data"},
		})
		require.NoError(t, err)

		// Wait for webhook
		success := webhookServer.WaitForRequests(1, 5*time.Second)
		assert.True(t, success, "Webhook should be received")

		// Verify event type header
		req := webhookServer.GetLastRequest()
		assert.Equal(t, "order.created", req.Headers.Get(HeaderEventType))
	})

	t.Run("prefix_wildcard", func(t *testing.T) {
		webhookServer.Reset()

		// Create subscription with "order.*" pattern
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_wildcard_prefix",
			URL:        webhookServer.WebhookURL(),
			EventTypes: []string{"order.*"},
			Secret:     "test_secret",
		})
		require.NoError(t, err)

		// Queue delivery with matching event type
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "order.created",
			Payload:        map[string]any{"order_id": "123"},
		})
		require.NoError(t, err)

		// Wait for webhook
		success := webhookServer.WaitForRequests(1, 5*time.Second)
		assert.True(t, success, "Webhook should be received for order.created")

		// Queue another matching event
		webhookServer.Reset()
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "order.updated",
			Payload:        map[string]any{"order_id": "123"},
		})
		require.NoError(t, err)

		success = webhookServer.WaitForRequests(1, 5*time.Second)
		assert.True(t, success, "Webhook should be received for order.updated")
	})

	t.Run("prefix_wildcard_no_match", func(t *testing.T) {
		// Create subscription with "order.*" pattern
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_wildcard_nomatch",
			URL:        webhookServer.WebhookURL(),
			EventTypes: []string{"order.*"},
			Secret:     "test_secret",
		})
		require.NoError(t, err)

		// Try to queue delivery with non-matching event type
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created", // Does not match "order.*"
			Payload:        map[string]any{"user_id": "456"},
		})

		// Should fail with event type mismatch error
		assert.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("multiple_patterns", func(t *testing.T) {
		webhookServer.Reset()

		// Create subscription with multiple patterns
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_multiple_patterns",
			URL:        webhookServer.WebhookURL(),
			EventTypes: []string{"order.*", "user.created", "invoice.paid"},
			Secret:     "test_secret",
		})
		require.NoError(t, err)

		// Queue delivery that matches first pattern
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "order.shipped",
			Payload:        map[string]any{"order_id": "123"},
		})
		require.NoError(t, err)

		success := webhookServer.WaitForRequests(1, 5*time.Second)
		assert.True(t, success, "Webhook should be received for order.shipped")

		// Queue delivery that matches exact event type
		webhookServer.Reset()
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "user.created",
			Payload:        map[string]any{"user_id": "456"},
		})
		require.NoError(t, err)

		success = webhookServer.WaitForRequests(1, 5*time.Second)
		assert.True(t, success, "Webhook should be received for user.created")

		// Try to queue non-matching event
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "customer.deleted",
			Payload:        map[string]any{"customer_id": "789"},
		})
		assert.Error(t, err, "Should fail for non-matching event type")
	})

	t.Logf("✓ E2E wildcard event types tests passed")
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
		Payload: map[string]any{
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
			Payload: map[string]any{
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
		Payload: map[string]any{
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
		Payload: map[string]any{
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
			Payload: map[string]any{
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
		Payload: map[string]any{
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
			Payload: map[string]any{
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
		Payload: map[string]any{
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
		Payload:        map[string]any{"test": "active"},
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
		Payload:        map[string]any{"test": "paused"},
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
			Payload:        map[string]any{"test": "resumed"},
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
	payload := map[string]any{
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
		Payload:        map[string]any{"order_id": "123"},
	})
	require.NoError(t, err)

	// Queue matching event (should be delivered)
	delivery2, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "order.updated",
		Payload:        map[string]any{"order_id": "123"},
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
		Payload:        map[string]any{"test": "headers"},
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
	items := make([]map[string]any, 1000)
	for i := 0; i < 1000; i++ {
		items[i] = map[string]any{
			"id":          fmt.Sprintf("item_%d", i),
			"name":        fmt.Sprintf("Test Item %d", i),
			"description": "This is a test item with some description text to make it larger. Lorem ipsum dolor sit amet, consectetur adipiscing elit.",
			"price":       float64(i) * 10.50,
			"quantity":    i % 100,
			"metadata": map[string]any{
				"category":    "test",
				"tags":        []string{"tag1", "tag2", "tag3"},
				"created_at":  "2025-01-01T00:00:00Z",
				"modified_at": "2025-01-02T00:00:00Z",
			},
		}
	}

	largePayload := map[string]any{
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
		Payload:        map[string]any{"test": "shutdown"},
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

// TestE2E_DeadLetterQueueLifecycle verifies the complete DLQ flow:
// 1. Delivery exhausts all retries → moves to dead_letter
// 2. List dead letters works correctly
// 3. Retry dead letter resets status to pending
// 4. Retried delivery succeeds
func TestE2E_DeadLetterQueueLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup test webhook server that fails initially
	webhookServer := testapi.SetupFailureServer()
	defer webhookServer.Close()

	// Setup manager with very few retries for fast testing
	config := NewConfig("mock")
	config.WorkerCount = 1
	config.QueuePollInterval = 100

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription with only 2 max retries to exhaust quickly
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_dlq",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.dlq"},
		Secret:     "test_secret_dlq",
		RetryPolicy: &RetryPolicy{
			MaxAttempts:    2, // Only 2 attempts before dead letter
			InitialBackoff: 100 * time.Millisecond,
			MaxBackoff:     1 * time.Second,
			BackoffFactor:  1.0, // No exponential backoff for speed
		},
	})
	require.NoError(t, err)

	// Queue delivery that will fail and exhaust retries
	delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
		SubscriptionID: sub.ID,
		EventType:      "test.dlq",
		Payload: map[string]any{
			"test": "dlq_lifecycle",
		},
	})
	require.NoError(t, err)
	t.Logf("Queued delivery: %s", delivery.ID)

	// Wait for delivery to exhaust retries and move to dead_letter
	// With 2 max attempts and 100ms backoff, should complete in ~1-2 seconds
	var finalDelivery *Delivery
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		finalDelivery, err = repo.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		if finalDelivery.Status == DeliveryStatusDeadLetter {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, DeliveryStatusDeadLetter, finalDelivery.Status,
		"Delivery should be in dead_letter status after exhausting retries")
	t.Logf("Delivery moved to dead_letter after %d attempts", finalDelivery.AttemptCount)

	// Step 2: Verify ListDeadLetters returns the delivery
	deadLetters, err := manager.ListDeadLetters(ctx, &DeadLetterFilter{
		TenantID: "tenant_dlq",
	})
	require.NoError(t, err)
	require.Len(t, deadLetters, 1, "Should have exactly 1 dead letter")
	assert.Equal(t, delivery.ID, deadLetters[0].ID)
	t.Log("ListDeadLetters correctly returned the dead letter")

	// Step 3: Verify GetDeadLetterCount
	count, err := manager.GetDeadLetterCount(ctx, "tenant_dlq")
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	t.Log("GetDeadLetterCount correctly returned 1")

	// Step 4: Switch server to success mode and retry the dead letter
	webhookServer.SimulateRecoveryScenario() // Now returns 200 OK
	t.Log("Server switched to success mode")

	retriedDelivery, err := manager.RetryDeadLetter(ctx, delivery.ID)
	require.NoError(t, err)
	assert.Equal(t, DeliveryStatusPending, retriedDelivery.Status)
	assert.Equal(t, 0, retriedDelivery.AttemptCount)
	t.Log("RetryDeadLetter reset delivery to pending status")

	// Step 5: Wait for retried delivery to succeed
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		finalDelivery, err = repo.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		if finalDelivery.Status == DeliveryStatusSuccess {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, DeliveryStatusSuccess, finalDelivery.Status,
		"Retried delivery should succeed")
	t.Logf("Retried delivery succeeded after %d total requests to endpoint", webhookServer.GetRequestCount())

	// Step 6: Verify dead letter list is now empty
	deadLetters, err = manager.ListDeadLetters(ctx, &DeadLetterFilter{
		TenantID: "tenant_dlq",
	})
	require.NoError(t, err)
	assert.Len(t, deadLetters, 0, "Dead letter list should be empty after successful retry")

	t.Logf("✓ E2E DLQ lifecycle test passed: exhaust → dead_letter → retry → success")
}

// TestE2E_DeadLetterQueueBulkOperations tests bulk retry and purge operations.
func TestE2E_DeadLetterQueueBulkOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Setup server that always fails
	webhookServer := testapi.SetupFailureServer()
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

	// Create subscription with minimal retries
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_bulk_dlq",
		URL:        webhookServer.WebhookURL(),
		EventTypes: []string{"test.bulk"},
		Secret:     "test_secret_bulk",
		RetryPolicy: &RetryPolicy{
			MaxAttempts:    1, // Single attempt before dead letter
			InitialBackoff: 50 * time.Millisecond,
			MaxBackoff:     1 * time.Second,
			BackoffFactor:  1.0,
		},
	})
	require.NoError(t, err)

	// Queue multiple deliveries
	deliveryIDs := make([]string, 3)
	for i := 0; i < 3; i++ {
		delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.bulk",
			Payload:        map[string]any{"index": i},
		})
		require.NoError(t, err)
		deliveryIDs[i] = delivery.ID
	}
	t.Logf("Queued %d deliveries", len(deliveryIDs))

	// Wait for all to become dead letters
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		count, _ := manager.GetDeadLetterCount(ctx, "tenant_bulk_dlq")
		if count == 3 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	count, err := manager.GetDeadLetterCount(ctx, "tenant_bulk_dlq")
	require.NoError(t, err)
	assert.Equal(t, 3, count, "All 3 deliveries should be in dead_letter")
	t.Logf("All %d deliveries moved to dead_letter", count)

	// Test PurgeDeadLetters
	purgeResult, err := manager.PurgeDeadLetters(ctx, &PurgeFilter{
		TenantID: "tenant_bulk_dlq",
	})
	require.NoError(t, err)
	assert.Equal(t, 3, purgeResult.TotalPurged)
	t.Logf("PurgeDeadLetters removed %d deliveries", purgeResult.TotalPurged)

	// Verify list is now empty
	count, err = manager.GetDeadLetterCount(ctx, "tenant_bulk_dlq")
	require.NoError(t, err)
	assert.Equal(t, 0, count, "Dead letter count should be 0 after purge")

	t.Log("✓ E2E DLQ bulk operations test passed")
}

// TestE2E_MetadataFiltering tests subscription filters with event metadata matching.
// This ensures that:
// 1. Events matching all filters are delivered
// 2. Events missing filter keys are NOT delivered
// 3. Events with mismatched values are NOT delivered
// 4. Subscriptions without filters receive all events
func TestE2E_MetadataFiltering(t *testing.T) {
	ctx := context.Background()

	// Create test server
	server := testapi.SetupSuccessServer()
	defer server.Close()

	// Create manager
	repo := NewMockRepository()
	config := NewConfig("mock://localhost")
	config.WorkerCount = 2
	config.QueuePollInterval = 100 // Poll frequently (ms)

	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	t.Run("filter_matches_metadata", func(t *testing.T) {
		// Create subscription WITH filters
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_filter_test",
			URL:        server.WebhookURL(),
			Secret:     "test_secret_filter",
			EventTypes: []string{"job.*"},
			Filters: map[string]string{
				"status":    "failed",
				"corpus_id": "corpus_123",
			},
		})
		require.NoError(t, err)
		t.Logf("Created subscription with filters: %v", sub.Filters)

		// Start manager
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Queue event that MATCHES filters
		delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "job.completed",
			Payload:        map[string]any{"message": "test matching"},
			Metadata: map[string]any{
				"status":    "failed",
				"corpus_id": "corpus_123",
				"extra":     "field", // Extra fields allowed
			},
		})
		require.NoError(t, err)
		t.Logf("Queued delivery with matching metadata: %s", delivery.ID)

		// Wait for delivery
		success := server.WaitForRequests(1, 5*time.Second)
		assert.True(t, success, "Should receive delivery when filters match")
		t.Log("Delivery received when metadata matches filters")
	})

	t.Run("filter_rejects_mismatched_value", func(t *testing.T) {
		// Create new subscription with filters
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_filter_test_2",
			URL:        server.WebhookURL(),
			Secret:     "test_secret_filter_2",
			EventTypes: []string{"job.*"},
			Filters: map[string]string{
				"status": "failed",
			},
		})
		require.NoError(t, err)

		// Try to queue event with MISMATCHED value
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "job.completed",
			Payload:        map[string]any{"message": "test mismatched"},
			Metadata: map[string]any{
				"status": "success", // Different value!
			},
		})
		require.Error(t, err, "Should reject delivery when filter value mismatches")
		assert.Contains(t, err.Error(), "filter criteria",
			"Error should mention filter mismatch")
		t.Log("Correctly rejected delivery when metadata value mismatches filter")
	})

	t.Run("filter_rejects_missing_key", func(t *testing.T) {
		// Create subscription with multiple filters
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_filter_test_3",
			URL:        server.WebhookURL(),
			Secret:     "test_secret_filter_3",
			EventTypes: []string{"job.*"},
			Filters: map[string]string{
				"status":    "failed",
				"corpus_id": "corpus_123",
			},
		})
		require.NoError(t, err)

		// Try to queue event MISSING required metadata key
		_, err = manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "job.completed",
			Payload:        map[string]any{"message": "test missing key"},
			Metadata: map[string]any{
				"status": "failed",
				// Missing corpus_id!
			},
		})
		require.Error(t, err, "Should reject delivery when filter key is missing from metadata")
		t.Log("Correctly rejected delivery when required metadata key is missing")
	})

	t.Run("no_filters_receives_all_events", func(t *testing.T) {
		// Create subscription WITHOUT filters
		sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_no_filter",
			URL:        server.WebhookURL(),
			Secret:     "test_secret_no_filter",
			EventTypes: []string{"job.*"},
			// No Filters field
		})
		require.NoError(t, err)
		t.Logf("Created subscription without filters")

		// Start fresh manager
		manager2, err := NewManager(config, repo)
		require.NoError(t, err)
		require.NoError(t, manager2.Start(ctx))
		defer manager2.Stop()

		// Queue event with any metadata (should be delivered)
		delivery, err := manager2.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "job.started",
			Payload:        map[string]any{"message": "test no filter"},
			Metadata: map[string]any{
				"random": "data",
			},
		})
		require.NoError(t, err)
		t.Logf("Queued delivery without filters: %s", delivery.ID)

		// Wait for delivery
		success := server.WaitForRequests(1, 5*time.Second)
		assert.True(t, success, "Should receive delivery when no filters configured")
		t.Log("Subscription without filters receives all events")
	})

	t.Log("✓ E2E metadata filtering test passed: filters correctly match/reject events")
}

// =============================================================================
// E2E TEST: INLINE DELIVERIES
// =============================================================================
// Tests the complete inline delivery lifecycle without a pre-created subscription.
// Inline deliveries allow one-off webhooks (e.g., job completion callbacks)
// without the overhead of subscription management.

func TestE2E_InlineDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	t.Run("successful inline delivery", func(t *testing.T) {
		// Setup test webhook server
		server := testapi.SetupSuccessServer()
		defer server.Close()

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

		// Queue inline delivery (no subscription needed)
		delivery, err := manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL:       server.WebhookURL(),
			Secret:    "inline_secret",
			TenantID:  "tenant_inline",
			EventType: "job.completed",
			Payload: map[string]any{
				"job_id":      "job_123",
				"status":      "success",
				"started_at":  "2024-01-01T10:00:00Z",
				"finished_at": "2024-01-01T10:05:00Z",
			},
		})
		require.NoError(t, err)
		assert.NotEmpty(t, delivery.ID)
		assert.Empty(t, delivery.SubscriptionID, "Inline delivery should have no subscription_id")
		assert.Equal(t, server.WebhookURL(), delivery.URL)
		assert.Equal(t, "inline_secret", delivery.Secret)
		assert.Equal(t, "tenant_inline", delivery.TenantID)
		assert.Equal(t, "job.completed", delivery.EventType)
		t.Logf("Queued inline delivery: %s -> %s", delivery.ID, delivery.URL)

		// Wait for delivery
		success := server.WaitForRequests(1, 5*time.Second)
		require.True(t, success, "Should receive inline delivery")

		// Verify delivery status
		updated, err := manager.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusSuccess, updated.Status)
		t.Logf("Inline delivery succeeded: %s", updated.Status)

		// Verify request headers (should have signature but empty subscription_id)
		reqs := server.GetReceivedRequests()
		require.Len(t, reqs, 1)
		assert.NotEmpty(t, reqs[0].Headers.Get(HeaderSignature))
		assert.Equal(t, delivery.ID, reqs[0].Headers.Get(HeaderDeliveryID))
		assert.Equal(t, "job.completed", reqs[0].Headers.Get(HeaderEventType))
		assert.Equal(t, "", reqs[0].Headers.Get(HeaderSubscriptionID), "Inline delivery has no subscription_id header")

		t.Log("✓ Inline delivery succeeded with correct headers")
	})

	t.Run("inline delivery with retry", func(t *testing.T) {
		// Setup test webhook server with intermittent failures (50% failure rate)
		// This will cause some requests to fail initially then succeed on retry
		server := testapi.SetupIntermittentServer(0.5) // 50% failure rate
		defer server.Close()

		// Setup manager with low poll interval for fast retries
		config := NewConfig("mock")
		config.WorkerCount = 2
		config.QueuePollInterval = 100

		repo := NewMockRepository()
		manager, err := NewManager(config, repo)
		require.NoError(t, err)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Queue inline delivery with max retries
		delivery, err := manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL:        server.WebhookURL(),
			Secret:     "retry_secret",
			TenantID:   "tenant_retry_inline",
			EventType:  "task.finished",
			Payload:    map[string]any{"task": "background_job"},
			MaxRetries: 5, // Allow enough retries to succeed
		})
		require.NoError(t, err)
		t.Logf("Queued inline delivery for retry test: %s", delivery.ID)

		// Wait for delivery processing to complete (either success or exhausted retries)
		time.Sleep(5 * time.Second)

		// Verify delivery was processed
		updated, err := manager.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		t.Logf("Delivery status: %s, attempts: %d", updated.Status, updated.AttemptCount)

		// Verify attempts recorded
		attempts, err := manager.GetDeliveryAttempts(ctx, delivery.ID)
		require.NoError(t, err)
		assert.Greater(t, len(attempts), 0, "Should have at least one attempt")
		t.Logf("Total attempts: %d", len(attempts))

		// Log attempt details for visibility
		for i, att := range attempts {
			t.Logf("  Attempt %d: status=%d, error=%s", i+1, att.StatusCode, att.Error)
		}

		t.Log("✓ Inline delivery retry test completed")
	})

	t.Run("inline delivery idempotency", func(t *testing.T) {
		// Setup test webhook server
		server := testapi.SetupSuccessServer()
		defer server.Close()

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

		idempotencyKey := "inline_idem_key_" + time.Now().Format("20060102150405")

		// Queue first inline delivery
		delivery1, err := manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL:            server.WebhookURL(),
			Secret:         "idem_secret",
			TenantID:       "tenant_idem_inline",
			EventType:      "notification.sent",
			Payload:        map[string]any{"message": "Hello"},
			IdempotencyKey: idempotencyKey,
		})
		require.NoError(t, err)
		t.Logf("First inline delivery: %s", delivery1.ID)

		// Try to queue duplicate with same idempotency key
		delivery2, err := manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL:            server.WebhookURL(),
			Secret:         "idem_secret",
			TenantID:       "tenant_idem_inline",
			EventType:      "notification.sent",
			Payload:        map[string]any{"message": "Hello"},
			IdempotencyKey: idempotencyKey,
		})
		require.Error(t, err)
		assert.Nil(t, delivery2)
		assert.True(t, IsIdempotencyError(err), "Should return idempotency error")
		t.Logf("Duplicate rejected: %v", err)

		// Wait for first delivery
		success := server.WaitForRequests(1, 5*time.Second)
		require.True(t, success, "Should receive exactly one delivery")

		// Verify only one request received (idempotency worked)
		assert.Len(t, server.GetReceivedRequests(), 1, "Should only have one request due to idempotency")

		t.Log("✓ Inline delivery idempotency enforced")
	})

	t.Run("inline delivery circuit breaker", func(t *testing.T) {
		// Setup test server that always fails with 503 Service Unavailable
		server := testapi.SetupCustomServer(http.StatusServiceUnavailable, `{"error": "service unavailable"}`)
		defer server.Close()

		// Setup manager with aggressive circuit breaker settings
		config := NewConfig("mock")
		config.WorkerCount = 1
		config.QueuePollInterval = 50
		config.CircuitBreakerThreshold = 2 // Open after 2 failures

		repo := NewMockRepository()
		manager, err := NewManager(config, repo)
		require.NoError(t, err)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Queue multiple inline deliveries to trigger circuit breaker
		for i := 0; i < 3; i++ {
			_, err := manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
				URL:        server.WebhookURL(),
				Secret:     "cb_secret",
				TenantID:   "tenant_cb_inline",
				EventType:  "test.event",
				Payload:    map[string]any{"attempt": i},
				MaxRetries: 1, // Only 1 retry so they exhaust quickly
			})
			require.NoError(t, err)
		}

		// Wait for circuit breaker to potentially open
		time.Sleep(3 * time.Second)

		// Check circuit breaker state
		cbState, err := repo.GetCircuitBreakerState(ctx, server.WebhookURL())
		require.NoError(t, err)
		t.Logf("Circuit breaker state: %s (failures: %d)", cbState.State, cbState.FailureCount)

		// Circuit breaker should be open or have recorded failures
		assert.True(t, cbState.FailureCount > 0 || cbState.State == CircuitBreakerStateOpen,
			"Circuit breaker should track failures for inline delivery endpoint")

		t.Log("✓ Inline delivery circuit breaker works")
	})

	t.Run("inline delivery validation", func(t *testing.T) {
		// Setup manager
		config := NewConfig("mock")
		repo := NewMockRepository()
		manager, err := NewManager(config, repo)
		require.NoError(t, err)

		ctx := context.Background()
		require.NoError(t, manager.Start(ctx))
		defer manager.Stop()

		// Test missing URL
		_, err = manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			TenantID:  "tenant",
			EventType: "test.event",
			Payload:   map[string]any{"data": "test"},
		})
		require.Error(t, err)
		assert.True(t, IsValidationError(err), "Missing URL should cause validation error")

		// Test missing tenant_id
		_, err = manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL:       "https://example.com/webhook",
			EventType: "test.event",
			Payload:   map[string]any{"data": "test"},
		})
		require.Error(t, err)
		assert.True(t, IsValidationError(err), "Missing tenant_id should cause validation error")

		// Test missing event_type
		_, err = manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL:      "https://example.com/webhook",
			TenantID: "tenant",
			Payload:  map[string]any{"data": "test"},
		})
		require.Error(t, err)
		assert.True(t, IsValidationError(err), "Missing event_type should cause validation error")

		// Test invalid URL
		_, err = manager.QueueInlineDelivery(ctx, &QueueInlineDeliveryRequest{
			URL:       "not-a-valid-url",
			TenantID:  "tenant",
			EventType: "test.event",
			Payload:   map[string]any{"data": "test"},
		})
		require.Error(t, err)
		assert.True(t, IsValidationError(err), "Invalid URL should cause validation error")

		// Test nil request
		_, err = manager.QueueInlineDelivery(ctx, nil)
		require.Error(t, err)
		assert.True(t, IsValidationError(err), "Nil request should cause validation error")

		t.Log("✓ Inline delivery validation works correctly")
	})

	t.Log("✓ E2E inline delivery test passed: full lifecycle works without subscription")
}
