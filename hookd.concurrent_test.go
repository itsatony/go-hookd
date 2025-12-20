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
// CONCURRENT WORKER POOL TESTS
// =============================================================================
// These tests verify that multiple workers can safely process deliveries
// concurrently without race conditions or duplicate processing.
//
// Critical behaviors tested:
// - SKIP LOCKED ensures no two workers get the same delivery
// - Worker semaphore correctly limits concurrency
// - No race conditions (run with: go test -race)
// - Deliveries are processed exactly once
// =============================================================================

func TestConcurrentWorkerPool_NoDoubleProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup PostgreSQL repository
	config := NewConfig("postgres://hookd:hookd@localhost:54321/hookd?sslmode=disable")
	repo, err := NewPostgresRepository(config.DatabaseURL)
	if err != nil {
		t.Skip(fmt.Sprintf("PostgreSQL not available: %v", err))
	}
	defer repo.Close()

	// Create test subscription
	ctx := context.Background()
	subID, err := GenerateSubscriptionID()
	require.NoError(t, err)

	sub := &Subscription{
		ID:         subID,
		TenantID:   "tenant_concurrent",
		URL:        "https://webhook.site/test-concurrent",
		EventTypes: []string{"test.concurrent"},
		Secret:     "test_secret",
		Status:     SubscriptionStatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	// Create 100 pending deliveries
	deliveryCount := 100
	deliveryIDs := make([]string, deliveryCount)

	for i := 0; i < deliveryCount; i++ {
		deliveryID, err := GenerateDeliveryID()
		require.NoError(t, err)

		delivery := &Delivery{
			ID:             deliveryID,
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.concurrent",
			Payload: map[string]any{
				"index": i,
				"test":  "concurrent",
			},
			Status:       DeliveryStatusPending,
			AttemptCount: 0,
			MaxAttempts:  3,
			CreatedAt:    time.Now(),
		}
		require.NoError(t, repo.CreateDelivery(ctx, delivery))
		deliveryIDs[i] = delivery.ID
	}

	// Start 10 concurrent workers that fetch and "process" deliveries
	workerCount := 10
	var wg sync.WaitGroup
	processedIDs := make([]string, 0, deliveryCount)
	var processedMu sync.Mutex
	var processedCount atomic.Int32

	// Channel to coordinate start of all workers
	startSignal := make(chan struct{})

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		workerID := w

		go func() {
			defer wg.Done()

			// Wait for start signal to ensure all workers start at the same time
			<-startSignal

			for {
				// Fetch pending deliveries using SKIP LOCKED
				deliveries, err := repo.GetPendingDeliveries(ctx, 10)
				if err != nil {
					t.Logf("Worker %d: Error fetching deliveries: %v", workerID, err)
					return
				}

				// No more deliveries to process
				if len(deliveries) == 0 {
					return
				}

				// "Process" each delivery (just mark as success)
				for _, delivery := range deliveries {
					// Record that we processed this delivery
					processedMu.Lock()
					processedIDs = append(processedIDs, delivery.ID)
					processedMu.Unlock()

					processedCount.Add(1)

					// Update delivery to success
					delivery.Status = DeliveryStatusSuccess
					now := time.Now()
					delivery.CompletedAt = &now
					delivery.AttemptCount++

					err := repo.UpdateDelivery(ctx, delivery)
					if err != nil {
						t.Logf("Worker %d: Error updating delivery %s: %v", workerID, delivery.ID, err)
					}

					// Simulate some processing time
					time.Sleep(10 * time.Millisecond)
				}
			}
		}()
	}

	// Start all workers at the same time
	close(startSignal)

	// Wait for all workers to complete
	wg.Wait()

	// Verify results
	t.Logf("Processed %d deliveries with %d workers", processedCount.Load(), workerCount)

	// All deliveries should be processed
	assert.Equal(t, int32(deliveryCount), processedCount.Load(),
		"All %d deliveries should be processed exactly once", deliveryCount)

	// Verify no duplicate processing
	processedMap := make(map[string]int)
	for _, id := range processedIDs {
		processedMap[id]++
	}

	duplicates := 0
	for id, count := range processedMap {
		if count > 1 {
			duplicates++
			t.Errorf("Delivery %s was processed %d times (should be 1)", id, count)
		}
	}

	assert.Equal(t, 0, duplicates, "No delivery should be processed more than once")

	// Verify all deliveries are marked as success
	for _, deliveryID := range deliveryIDs {
		delivery, err := repo.GetDelivery(ctx, deliveryID)
		require.NoError(t, err)
		assert.Equal(t, DeliveryStatusSuccess, delivery.Status,
			"Delivery %s should be marked as success", deliveryID)
	}

	// Cleanup
	for _, deliveryID := range deliveryIDs {
		_, _ = repo.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", TableDeliveries), deliveryID)
	}
	_, _ = repo.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", TableSubscriptions), sub.ID)
}

func TestConcurrentWorkerPool_SkipLocked(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup PostgreSQL repository
	config := NewConfig("postgres://hookd:hookd@localhost:54321/hookd?sslmode=disable")
	repo, err := NewPostgresRepository(config.DatabaseURL)
	if err != nil {
		t.Skip(fmt.Sprintf("PostgreSQL not available: %v", err))
	}
	defer repo.Close()

	// Create test subscription
	ctx := context.Background()
	subID, err := GenerateSubscriptionID()
	require.NoError(t, err)

	sub := &Subscription{
		ID:         subID,
		TenantID:   "tenant_skip_locked",
		URL:        "https://webhook.site/test-skip-locked",
		EventTypes: []string{"test.skip_locked"},
		Secret:     "test_secret",
		Status:     SubscriptionStatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	// Create 50 pending deliveries
	deliveryCount := 50
	deliveryIDs := make([]string, deliveryCount)

	for i := 0; i < deliveryCount; i++ {
		deliveryID, err := GenerateDeliveryID()
		require.NoError(t, err)

		delivery := &Delivery{
			ID:             deliveryID,
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.skip_locked",
			Payload: map[string]any{
				"index": i,
			},
			Status:       DeliveryStatusPending,
			AttemptCount: 0,
			MaxAttempts:  3,
			CreatedAt:    time.Now(),
		}
		require.NoError(t, repo.CreateDelivery(ctx, delivery))
		deliveryIDs[i] = delivery.ID
	}

	// Test: Two concurrent queries should return different deliveries
	// because SKIP LOCKED should prevent the second query from getting
	// deliveries that are locked by the first query

	var wg sync.WaitGroup
	worker1IDs := make([]string, 0)
	worker2IDs := make([]string, 0)
	var mu sync.Mutex

	// Start transaction for worker 1
	wg.Add(1)
	go func() {
		defer wg.Done()

		tx, err := repo.BeginTx(ctx)
		if err != nil {
			t.Logf("Worker 1: Failed to begin transaction: %v", err)
			return
		}
		defer tx.(*PostgresRepositoryTx).Rollback()

		// Worker 1 gets pending deliveries (locks them in transaction)
		deliveries, err := tx.GetPendingDeliveries(ctx, 25)
		if err != nil {
			t.Logf("Worker 1: Error fetching deliveries: %v", err)
			return
		}

		mu.Lock()
		for _, d := range deliveries {
			worker1IDs = append(worker1IDs, d.ID)
		}
		mu.Unlock()

		t.Logf("Worker 1 locked %d deliveries", len(deliveries))

		// Hold the transaction for 2 seconds
		time.Sleep(2 * time.Second)
	}()

	// Give worker 1 time to lock its deliveries
	time.Sleep(500 * time.Millisecond)

	// Start transaction for worker 2 (should skip locked rows)
	wg.Add(1)
	go func() {
		defer wg.Done()

		tx, err := repo.BeginTx(ctx)
		if err != nil {
			t.Logf("Worker 2: Failed to begin transaction: %v", err)
			return
		}
		defer tx.(*PostgresRepositoryTx).Rollback()

		// Worker 2 gets pending deliveries (should skip locked ones)
		deliveries, err := tx.GetPendingDeliveries(ctx, 25)
		if err != nil {
			t.Logf("Worker 2: Error fetching deliveries: %v", err)
			return
		}

		mu.Lock()
		for _, d := range deliveries {
			worker2IDs = append(worker2IDs, d.ID)
		}
		mu.Unlock()

		t.Logf("Worker 2 got %d deliveries (skipped locked)", len(deliveries))
	}()

	wg.Wait()

	// Verify no overlap between worker1 and worker2 deliveries
	worker1Set := make(map[string]bool)
	for _, id := range worker1IDs {
		worker1Set[id] = true
	}

	overlaps := 0
	for _, id := range worker2IDs {
		if worker1Set[id] {
			overlaps++
			t.Errorf("Delivery %s was fetched by both workers (SKIP LOCKED failed)", id)
		}
	}

	assert.Equal(t, 0, overlaps, "SKIP LOCKED should prevent delivery overlap between workers")
	assert.Greater(t, len(worker1IDs), 0, "Worker 1 should have fetched deliveries")
	assert.Greater(t, len(worker2IDs), 0, "Worker 2 should have fetched deliveries (skipping locked ones)")

	// Cleanup
	for _, deliveryID := range deliveryIDs {
		_, _ = repo.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", TableDeliveries), deliveryID)
	}
	_, _ = repo.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", TableSubscriptions), sub.ID)
}

func TestConcurrentWorkerPool_WorkerSemaphore(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// This test verifies that the worker semaphore correctly limits concurrency
	maxWorkers := 5
	config := NewConfig("postgres://hookd:hookd@localhost:54321/hookd?sslmode=disable")
	config.WorkerCount = maxWorkers

	repo, err := NewPostgresRepository(config.DatabaseURL)
	if err != nil {
		t.Skip(fmt.Sprintf("PostgreSQL not available: %v", err))
	}
	defer repo.Close()

	// Create manager with limited workers
	manager, err := NewManager(config, repo)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, manager.Start(ctx))
	defer manager.Stop()

	// Create test subscription
	subID, err := GenerateSubscriptionID()
	require.NoError(t, err)

	sub := &Subscription{
		ID:         subID,
		TenantID:   "tenant_semaphore",
		URL:        "https://httpbin.org/delay/2", // Endpoint that takes 2 seconds
		EventTypes: []string{"test.semaphore"},
		Secret:     "test_secret",
		Status:     SubscriptionStatusActive,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	// Create 20 deliveries (more than worker count)
	deliveryCount := 20
	deliveryIDs := make([]string, deliveryCount)

	for i := 0; i < deliveryCount; i++ {
		deliveryID, err := GenerateDeliveryID()
		require.NoError(t, err)

		delivery := &Delivery{
			ID:             deliveryID,
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.semaphore",
			Payload: map[string]any{
				"index": i,
			},
			Status:       DeliveryStatusPending,
			AttemptCount: 0,
			MaxAttempts:  1,
			CreatedAt:    time.Now(),
		}
		require.NoError(t, repo.CreateDelivery(ctx, delivery))
		deliveryIDs[i] = delivery.ID
	}

	// Wait for deliveries to be processed
	// With 5 workers and 2s delay, 20 deliveries should take at least 8 seconds
	// (20 / 5 = 4 batches × 2s = 8s)
	startTime := time.Now()

	// Wait up to 30 seconds for all deliveries to complete
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	completedCount := 0
	for completedCount < deliveryCount {
		select {
		case <-timeout:
			t.Fatalf("Timeout waiting for deliveries to complete (got %d/%d)", completedCount, deliveryCount)
		case <-ticker.C:
			completed := 0
			for _, deliveryID := range deliveryIDs {
				delivery, err := repo.GetDelivery(ctx, deliveryID)
				if err == nil && (delivery.Status == DeliveryStatusSuccess || delivery.Status == DeliveryStatusFailed) {
					completed++
				}
			}
			completedCount = completed
			t.Logf("Progress: %d/%d deliveries completed", completedCount, deliveryCount)
		}
	}

	duration := time.Since(startTime)
	t.Logf("Processed %d deliveries in %v with %d workers", deliveryCount, duration, maxWorkers)

	// Verify that processing took a reasonable amount of time
	// With proper semaphore limiting, it should take at least 8 seconds
	// (but we'll be lenient and check for 6 seconds due to overhead)
	minExpectedDuration := 6 * time.Second
	assert.GreaterOrEqual(t, duration, minExpectedDuration,
		"Processing should take at least %v with %d workers (actual: %v)",
		minExpectedDuration, maxWorkers, duration)

	// Cleanup
	for _, deliveryID := range deliveryIDs {
		_, _ = repo.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", TableDeliveries), deliveryID)
	}
	_, _ = repo.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", TableSubscriptions), sub.ID)
}
