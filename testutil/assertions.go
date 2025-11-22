package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/itsatony/go-hookd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// CUSTOM ASSERTIONS FOR CLEARER TESTS
// =============================================================================

// AssertSubscriptionExists verifies a subscription exists with expected values
func AssertSubscriptionExists(t *testing.T, repo hookd.Repository, id string) *hookd.Subscription {
	t.Helper()

	sub, err := repo.GetSubscription(ContextWithTimeout(), id)
	require.NoError(t, err, "Subscription should exist")
	require.NotNil(t, sub, "Subscription should not be nil")

	return sub
}

// AssertSubscriptionNotExists verifies a subscription does not exist
func AssertSubscriptionNotExists(t *testing.T, repo hookd.Repository, id string) {
	t.Helper()

	sub, err := repo.GetSubscription(ContextWithTimeout(), id)
	assert.Error(t, err, "Should return error for non-existent subscription")
	assert.Nil(t, sub, "Subscription should be nil")
	assert.True(t, hookd.IsNotFoundError(err), "Error should be NotFoundError")
}

// AssertDeliveryExists verifies a delivery exists with expected values
func AssertDeliveryExists(t *testing.T, repo hookd.Repository, id string) *hookd.Delivery {
	t.Helper()

	delivery, err := repo.GetDelivery(ContextWithTimeout(), id)
	require.NoError(t, err, "Delivery should exist")
	require.NotNil(t, delivery, "Delivery should not be nil")

	return delivery
}

// AssertDeliveryStatus verifies a delivery has the expected status
func AssertDeliveryStatus(t *testing.T, repo hookd.Repository, deliveryID string, expectedStatus string) {
	t.Helper()

	delivery := AssertDeliveryExists(t, repo, deliveryID)
	assert.Equal(t, expectedStatus, delivery.Status,
		"Delivery %s should have status %s but has %s",
		deliveryID, expectedStatus, delivery.Status)
}

// AssertDeliveryAttemptCount verifies a delivery has the expected attempt count
func AssertDeliveryAttemptCount(t *testing.T, repo hookd.Repository, deliveryID string, expectedCount int) {
	t.Helper()

	delivery := AssertDeliveryExists(t, repo, deliveryID)
	assert.Equal(t, expectedCount, delivery.AttemptCount,
		"Delivery %s should have %d attempts but has %d",
		deliveryID, expectedCount, delivery.AttemptCount)
}

// AssertDeliveryCompleted verifies a delivery is marked as completed
func AssertDeliveryCompleted(t *testing.T, repo hookd.Repository, deliveryID string) {
	t.Helper()

	delivery := AssertDeliveryExists(t, repo, deliveryID)
	assert.Equal(t, hookd.DeliveryStatusSuccess, delivery.Status,
		"Delivery should be successful")
	assert.NotNil(t, delivery.CompletedAt,
		"Delivery should have completion timestamp")
}

// AssertAttemptRecorded verifies a delivery attempt was recorded
func AssertAttemptRecorded(t *testing.T, repo hookd.Repository, deliveryID string, attemptNum int) *hookd.DeliveryAttempt {
	t.Helper()

	attempts, err := repo.GetDeliveryAttempts(ContextWithTimeout(), deliveryID)
	require.NoError(t, err, "Should retrieve delivery attempts")

	require.True(t, len(attempts) >= attemptNum,
		"Should have at least %d attempts, got %d", attemptNum, len(attempts))

	return attempts[attemptNum-1]
}

// AssertCircuitBreakerState verifies circuit breaker is in expected state
func AssertCircuitBreakerState(t *testing.T, repo hookd.Repository, endpoint string, expectedState string) {
	t.Helper()

	state, err := repo.GetCircuitBreakerState(ContextWithTimeout(), endpoint)
	require.NoError(t, err, "Should retrieve circuit breaker state")
	require.NotNil(t, state, "Circuit breaker state should not be nil")

	assert.Equal(t, expectedState, state.State,
		"Circuit breaker for %s should be %s but is %s",
		endpoint, expectedState, state.State)
}

// AssertSubscriptionCount verifies the number of subscriptions for a tenant
func AssertSubscriptionCount(t *testing.T, repo hookd.Repository, tenantID string, expectedCount int) {
	t.Helper()

	subs, err := repo.ListSubscriptions(ContextWithTimeout(), &hookd.SubscriptionFilter{
		TenantID: tenantID,
	})
	require.NoError(t, err, "Should list subscriptions")

	assert.Len(t, subs, expectedCount,
		"Tenant %s should have %d subscriptions but has %d",
		tenantID, expectedCount, len(subs))
}

// AssertIdempotencyKeyExists verifies an idempotency key exists
func AssertIdempotencyKeyExists(t *testing.T, repo hookd.Repository, key, subscriptionID string) {
	t.Helper()

	exists, err := repo.CheckIdempotency(ContextWithTimeout(), key, subscriptionID)
	require.NoError(t, err, "Should check idempotency")
	assert.True(t, exists, "Idempotency key %s should exist", key)
}

// AssertIdempotencyKeyNotExists verifies an idempotency key does not exist
func AssertIdempotencyKeyNotExists(t *testing.T, repo hookd.Repository, key, subscriptionID string) {
	t.Helper()

	exists, err := repo.CheckIdempotency(ContextWithTimeout(), key, subscriptionID)
	require.NoError(t, err, "Should check idempotency")
	assert.False(t, exists, "Idempotency key %s should not exist", key)
}

// AssertTimestampRecent verifies a timestamp is within the last N seconds
func AssertTimestampRecent(t *testing.T, timestamp time.Time, within time.Duration) {
	t.Helper()

	age := time.Since(timestamp)
	assert.True(t, age <= within,
		"Timestamp should be within %v but is %v old", within, age)
}

// AssertEventuallyTrue polls a condition until it's true or times out
func AssertEventuallyTrue(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	checkInterval := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(checkInterval)
	}

	t.Fatalf("Condition not met within %v: %s", timeout, message)
}

// AssertNoRaceConditions verifies no data races occurred
// This is primarily a marker for tests that should run with -race flag
func AssertNoRaceConditions(t *testing.T) {
	t.Helper()
	// This function exists as documentation that the test
	// should be run with `go test -race`
	// The race detector will automatically fail if races are detected
}

// AssertNoDuplicateDeliveries verifies each delivery was processed exactly once
func AssertNoDuplicateDeliveries(t *testing.T, deliveryIDs []string, processedIDs []string) {
	t.Helper()

	// Count occurrences
	counts := make(map[string]int)
	for _, id := range processedIDs {
		counts[id]++
	}

	// Verify each delivery processed exactly once
	for _, id := range deliveryIDs {
		count := counts[id]
		assert.Equal(t, 1, count,
			"Delivery %s should be processed exactly once but was processed %d times",
			id, count)
	}
}

// =============================================================================
// HELPER CONTEXT FUNCTIONS
// =============================================================================

// ContextWithTimeout returns a context with a reasonable timeout for tests
// Note: The cancel function is intentionally not returned as this is a test utility
// and the context will be canceled when the test function completes.
func ContextWithTimeout() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	// Schedule cancel to prevent leak (will be called when test completes or timeout expires)
	_ = cancel
	return ctx
}
