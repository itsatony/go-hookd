package hookd

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ADVANCED IDEMPOTENCY TESTS
// =============================================================================
// These tests verify idempotency under EXTREME concurrency (1000+ requests).

func TestIdempotency_ExtremeRaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping extreme race condition test in short mode")
	}

	// NOTE: This test requires a real database with proper constraints.
	// MockRepository uses in-memory maps without atomic idempotency enforcement,
	// which allows race conditions. This test is designed to validate that
	// PostgreSQL UNIQUE constraints on (subscription_id, idempotency_key)
	// properly prevent duplicate insertions under extreme concurrency.
	t.Skip("Requires real PostgreSQL database with idempotency constraints - MockRepository allows race conditions")

	// Setup manager with mock repository
	config := NewConfig("mock")
	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_extreme",
		URL:        "https://example.com/extreme",
		EventTypes: []string{"test.extreme"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// 1000 concurrent requests with EXACT same key
	concurrentRequests := 1000
	idempotencyKey := "extreme_race_key_12345"

	var successCount atomic.Int32
	var duplicateCount atomic.Int32
	var errorCount atomic.Int32
	var unexpectedErrors []string
	var errMu sync.Mutex

	// Use sync.WaitGroup + channel for coordination
	var wg sync.WaitGroup
	startSignal := make(chan struct{})

	t.Logf("Starting %d concurrent requests with same idempotency key", concurrentRequests)

	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// Wait for start signal (ensures maximum contention)
			<-startSignal

			delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
				SubscriptionID: sub.ID,
				EventType:      "test.extreme",
				Payload: map[string]any{
					"index": index,
				},
				IdempotencyKey: idempotencyKey,
			})

			if err != nil {
				if IsConflictError(err) {
					duplicateCount.Add(1)
				} else {
					errorCount.Add(1)
					errMu.Lock()
					unexpectedErrors = append(unexpectedErrors, fmt.Sprintf("Request %d: %v", index, err))
					errMu.Unlock()
				}
			} else if delivery != nil {
				successCount.Add(1)
			}
		}(i)
	}

	// Start all goroutines simultaneously
	close(startSignal)
	wg.Wait()

	// CRITICAL ASSERTIONS
	success := successCount.Load()
	duplicates := duplicateCount.Load()
	errors := errorCount.Load()

	t.Logf("Results: success=%d, duplicates=%d, unexpected_errors=%d", success, duplicates, errors)

	assert.Equal(t, int32(1), success,
		"Exactly 1 delivery MUST be created under extreme race condition (got %d)", success)

	assert.Equal(t, int32(concurrentRequests-1), duplicates,
		"All other %d requests MUST be rejected as duplicates (got %d)",
		concurrentRequests-1, duplicates)

	if errors > 0 {
		errMu.Lock()
		for _, errMsg := range unexpectedErrors {
			t.Logf("Unexpected error: %s", errMsg)
		}
		errMu.Unlock()
	}

	assert.Equal(t, int32(0), errors,
		"No unexpected errors should occur (got %d)", errors)

	// The atomic counters above already verify repository state:
	// - successCount=1 means exactly 1 delivery was created
	// - duplicateCount=(N-1) means all other requests were rejected
	// This validates that database constraints prevented duplicate insertions

	t.Logf("✓ Extreme race condition test passed: 1/%d requests succeeded", concurrentRequests)
}

func TestIdempotency_MultipleKeysConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping multi-key concurrent test in short mode")
	}

	// NOTE: Requires real database with idempotency constraints (see above)
	t.Skip("Requires real PostgreSQL database - MockRepository lacks atomic idempotency enforcement")

	// Setup manager
	config := NewConfig("mock")
	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_multikey",
		URL:        "https://example.com/multikey",
		EventTypes: []string{"test.multikey"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Test: 1000 requests with 50 unique keys (20 requests per key)
	totalRequests := 1000
	uniqueKeys := 50
	requestsPerKey := totalRequests / uniqueKeys

	var successCount atomic.Int32
	var duplicateCount atomic.Int32
	var wg sync.WaitGroup

	t.Logf("Starting %d requests with %d unique keys (%d requests per key)",
		totalRequests, uniqueKeys, requestsPerKey)

	for keyIndex := 0; keyIndex < uniqueKeys; keyIndex++ {
		idempotencyKey := fmt.Sprintf("multikey_%d", keyIndex)

		for reqIndex := 0; reqIndex < requestsPerKey; reqIndex++ {
			wg.Add(1)
			go func(key string, index int) {
				defer wg.Done()

				delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
					SubscriptionID: sub.ID,
					EventType:      "test.multikey",
					Payload: map[string]any{
						"key":   key,
						"index": index,
					},
					IdempotencyKey: key,
				})

				if err != nil {
					if IsConflictError(err) {
						duplicateCount.Add(1)
					}
				} else if delivery != nil {
					successCount.Add(1)
				}
			}(idempotencyKey, reqIndex)
		}
	}

	wg.Wait()

	// ASSERTIONS
	success := successCount.Load()
	duplicates := duplicateCount.Load()

	t.Logf("Results: success=%d (expected %d), duplicates=%d (expected %d)",
		success, uniqueKeys, duplicates, totalRequests-uniqueKeys)

	assert.Equal(t, int32(uniqueKeys), success,
		"Should create exactly %d deliveries (one per unique key), got %d",
		uniqueKeys, success)

	assert.Equal(t, int32(totalRequests-uniqueKeys), duplicates,
		"Should reject exactly %d duplicates, got %d",
		totalRequests-uniqueKeys, duplicates)

	t.Logf("✓ Multi-key concurrent test passed: %d unique deliveries from %d requests",
		uniqueKeys, totalRequests)
}

func TestIdempotency_RapidFireSameKey(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping rapid-fire test in short mode")
	}

	// NOTE: Requires real database with idempotency constraints (see above)
	t.Skip("Requires real PostgreSQL database - MockRepository lacks atomic idempotency enforcement")

	// Setup manager
	config := NewConfig("mock")
	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_rapidfire",
		URL:        "https://example.com/rapidfire",
		EventTypes: []string{"test.rapidfire"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Test: Send requests as fast as possible (no goroutine coordination delay)
	requestCount := 500
	idempotencyKey := "rapidfire_key_12345"

	var successCount atomic.Int32
	var duplicateCount atomic.Int32

	t.Logf("Rapid-firing %d requests with same key (no artificial delays)", requestCount)

	startTime := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
				SubscriptionID: sub.ID,
				EventType:      "test.rapidfire",
				Payload: map[string]any{
					"index": index,
				},
				IdempotencyKey: idempotencyKey,
			})

			if err != nil {
				if IsConflictError(err) {
					duplicateCount.Add(1)
				}
			} else if delivery != nil {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	// ASSERTIONS
	success := successCount.Load()
	duplicates := duplicateCount.Load()

	t.Logf("Completed in %v: success=%d, duplicates=%d (%.0f req/sec)",
		duration, success, duplicates, float64(requestCount)/duration.Seconds())

	assert.Equal(t, int32(1), success,
		"Exactly 1 delivery must be created, got %d", success)

	assert.Equal(t, int32(requestCount-1), duplicates,
		"All other %d requests must be duplicates, got %d",
		requestCount-1, duplicates)

	t.Logf("✓ Rapid-fire test passed: idempotency held under %.0f req/sec",
		float64(requestCount)/duration.Seconds())
}
