package internal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ADVANCED CONCURRENT WORKER TESTS
// =============================================================================
// These tests verify worker semaphore ACTUALLY limits concurrency
// by tracking concurrent execution with atomic counters.

func TestWorkerSemaphore_ActualConcurrencyLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping advanced concurrency test in short mode")
	}

	maxWorkers := 5
	deliveryCount := 25 // 5x worker count to ensure saturation

	// Track concurrent execution
	var currentConcurrency atomic.Int32
	var maxConcurrency atomic.Int32
	var mu sync.Mutex
	concurrencySnapshots := []int32{}

	// Create test server that tracks concurrency
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Increment on entry
		current := currentConcurrency.Add(1)

		// Track max
		max := maxConcurrency.Load()
		if current > max {
			maxConcurrency.Store(current)
		}

		// Sample concurrency
		mu.Lock()
		concurrencySnapshots = append(concurrencySnapshots, current)
		mu.Unlock()

		// Hold for 200ms to ensure overlap
		time.Sleep(200 * time.Millisecond)

		// Decrement on exit
		currentConcurrency.Add(-1)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	// Setup manager
	config := NewConfig("mock")
	config.WorkerCount = maxWorkers
	config.QueuePollInterval = 50 // Fast polling
	config.DeliveryTimeoutMs = 5000

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_concurrency",
		URL:        server.URL,
		EventTypes: []string{"test.concurrency"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue deliveries
	deliveryIDs := make([]string, deliveryCount)
	for i := 0; i < deliveryCount; i++ {
		delivery, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.concurrency",
			Payload: map[string]interface{}{
				"index": i,
			},
		})
		require.NoError(t, err)
		deliveryIDs[i] = delivery.ID
	}

	t.Logf("Queued %d deliveries with max %d workers", deliveryCount, maxWorkers)

	// Wait for all deliveries to complete
	WaitForCondition(t, func() bool {
		completed := 0
		for _, id := range deliveryIDs {
			d, err := repo.GetDelivery(ctx, id)
			if err == nil && d.Status == DeliveryStatusSuccess {
				completed++
			}
		}
		return completed == deliveryCount
	}, 30*time.Second, fmt.Sprintf("All %d deliveries should complete", deliveryCount))

	// CRITICAL ASSERTIONS
	max := maxConcurrency.Load()
	t.Logf("Maximum observed concurrency: %d (limit: %d)", max, maxWorkers)

	assert.LessOrEqual(t, max, int32(maxWorkers),
		"Maximum concurrent workers (%d) MUST NOT exceed limit (%d). Semaphore is not enforcing limit!",
		max, maxWorkers)

	// Verify we actually hit the limit (not just staying below it)
	assert.Equal(t, int32(maxWorkers), max,
		"Maximum concurrency should equal worker count (semaphore working properly). Got %d, expected %d",
		max, maxWorkers)

	// Statistical check: most samples should be at or near max
	mu.Lock()
	atOrNearLimit := 0
	for _, count := range concurrencySnapshots {
		// Consider "near limit" as within 1 of max (accounts for timing variance)
		if count >= int32(maxWorkers-1) {
			atOrNearLimit++
		}
	}
	totalSamples := len(concurrencySnapshots)
	mu.Unlock()

	t.Logf("Samples at/near limit: %d/%d (%.1f%%)", atOrNearLimit, totalSamples, float64(atOrNearLimit)*100/float64(totalSamples))

	assert.Greater(t, atOrNearLimit, totalSamples/3,
		"Expected >33%% of samples at/near max concurrency, got %.1f%%",
		float64(atOrNearLimit)*100/float64(totalSamples))

	t.Logf("✓ Worker semaphore correctly limits concurrency to %d", maxWorkers)
}

func TestWorkerSemaphore_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	maxWorkers := 10
	deliveryCount := 100

	// Track violations
	var violations atomic.Int32
	var maxConcurrency atomic.Int32
	var currentConcurrency atomic.Int32

	// Create fast-responding test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := currentConcurrency.Add(1)

		// Check for violations
		if current > int32(maxWorkers) {
			violations.Add(1)
		}

		// Track max
		max := maxConcurrency.Load()
		if current > max {
			maxConcurrency.Store(current)
		}

		// Small delay
		time.Sleep(50 * time.Millisecond)

		currentConcurrency.Add(-1)

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	// Setup manager
	config := NewConfig("mock")
	config.WorkerCount = maxWorkers
	config.QueuePollInterval = 20 // Very fast polling for stress test

	repo := NewMockRepository()
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create subscription
	sub, err := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
		TenantID:   "tenant_stress",
		URL:        server.URL,
		EventTypes: []string{"test.stress"},
		Secret:     "test_secret",
	})
	require.NoError(t, err)

	// Queue all deliveries rapidly
	for i := 0; i < deliveryCount; i++ {
		_, err := manager.QueueDelivery(ctx, &QueueDeliveryRequest{
			SubscriptionID: sub.ID,
			EventType:      "test.stress",
			Payload: map[string]interface{}{
				"index": i,
			},
		})
		require.NoError(t, err)
	}

	t.Logf("Queued %d deliveries rapidly", deliveryCount)

	// Wait for completion
	time.Sleep(10 * time.Second)

	// CRITICAL ASSERTION
	violationCount := violations.Load()
	maxObserved := maxConcurrency.Load()

	t.Logf("Stress test results: max concurrency=%d, violations=%d", maxObserved, violationCount)

	assert.Equal(t, int32(0), violationCount,
		"NO concurrency violations allowed! Observed %d violations with max=%d (limit=%d)",
		violationCount, maxObserved, maxWorkers)

	assert.LessOrEqual(t, maxObserved, int32(maxWorkers),
		"Maximum concurrency %d exceeded limit %d", maxObserved, maxWorkers)

	t.Logf("✓ Stress test passed: no violations under rapid load")
}
