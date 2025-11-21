// Package internal provides the core webhook management implementation for go-hookd.
//
// This file contains integration tests for the PostgresRepository implementation.
// These tests require a running PostgreSQL database and will be skipped if unavailable.
//
// To run these tests:
//  1. Start the database: ./scripts/db-dev.sh bootstrap
//  2. Run tests: go test -v -run TestPostgresRepository ./internal/...
//  3. Or set HOOKD_TEST_DB env var to a custom connection string
package internal

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getTestConnectionString returns the database connection string for tests.
// Checks HOOKD_TEST_DB environment variable first, falls back to default dev database.
func getTestConnectionString() string {
	if connStr := os.Getenv("HOOKD_TEST_DB"); connStr != "" {
		return connStr
	}
	// Default dev database from docker-compose
	return "postgresql://hookd_dev:hookd_dev_password_change_in_production@localhost:5433/hookd_dev?sslmode=disable"
}

// setupTestDB creates a test database connection and runs migrations.
// Returns a cleanup function that should be called after tests complete.
func setupTestDB(t *testing.T) (*PostgresRepository, func()) {
	t.Helper()

	connStr := getTestConnectionString()

	// Try to connect to verify database is available
	repo, err := NewPostgresRepository(connStr)
	if err != nil {
		t.Skipf("PostgreSQL database not available (run ./scripts/db-dev.sh bootstrap): %v", err)
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := repo.Ping(ctx); err != nil {
		repo.Close()
		t.Skipf("PostgreSQL database not responding: %v", err)
		return nil, nil
	}

	// Cleanup function
	cleanup := func() {
		// Clean up all test data
		ctx := context.Background()
		repo.db.ExecContext(ctx, "TRUNCATE TABLE delivery_attempts CASCADE")
		repo.db.ExecContext(ctx, "TRUNCATE TABLE deliveries CASCADE")
		repo.db.ExecContext(ctx, "TRUNCATE TABLE idempotency_store CASCADE")
		repo.db.ExecContext(ctx, "TRUNCATE TABLE circuit_breaker_state CASCADE")
		repo.db.ExecContext(ctx, "TRUNCATE TABLE subscriptions CASCADE")
		repo.Close()
	}

	// Clean tables before tests
	ctx = context.Background()
	repo.db.ExecContext(ctx, "TRUNCATE TABLE delivery_attempts CASCADE")
	repo.db.ExecContext(ctx, "TRUNCATE TABLE deliveries CASCADE")
	repo.db.ExecContext(ctx, "TRUNCATE TABLE idempotency_store CASCADE")
	repo.db.ExecContext(ctx, "TRUNCATE TABLE circuit_breaker_state CASCADE")
	repo.db.ExecContext(ctx, "TRUNCATE TABLE subscriptions CASCADE")

	return repo, cleanup
}

// =============================================================================
// SUBSCRIPTION TESTS
// =============================================================================

func TestPostgresRepository_SubscriptionCRUD(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("Create and Get", func(t *testing.T) {
		sub := createTestSubscription(t, "sub_pg_test1", "tenant1", "https://example.com/webhook1")

		err := repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		retrieved, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, sub.ID, retrieved.ID)
		assert.Equal(t, sub.TenantID, retrieved.TenantID)
		assert.Equal(t, sub.URL, retrieved.URL)
		assert.Equal(t, sub.Status, retrieved.Status)
		assert.ElementsMatch(t, sub.EventTypes, retrieved.EventTypes)
	})

	t.Run("Duplicate tenant+url", func(t *testing.T) {
		sub1 := createTestSubscription(t, "sub_pg_test2", "tenant2", "https://example.com/webhook2")
		require.NoError(t, repo.CreateSubscription(ctx, sub1))

		sub2 := createTestSubscription(t, "sub_pg_test3", "tenant2", "https://example.com/webhook2")
		err := repo.CreateSubscription(ctx, sub2)
		assert.ErrorIs(t, err, ErrDuplicateSubscription)
	})

	t.Run("Update", func(t *testing.T) {
		sub := createTestSubscription(t, "sub_pg_test4", "tenant3", "https://example.com/webhook3")
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		sub.Status = SubscriptionStatusPaused
		sub.URL = "https://example.com/webhook3-updated"
		err := repo.UpdateSubscription(ctx, sub)
		require.NoError(t, err)

		retrieved, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, SubscriptionStatusPaused, retrieved.Status)
		assert.Equal(t, "https://example.com/webhook3-updated", retrieved.URL)
	})

	t.Run("Delete", func(t *testing.T) {
		sub := createTestSubscription(t, "sub_pg_test5", "tenant4", "https://example.com/webhook4")
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		err := repo.DeleteSubscription(ctx, sub.ID)
		require.NoError(t, err)

		_, err = repo.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("List with filters", func(t *testing.T) {
		// Create test subscriptions
		sub1 := createTestSubscription(t, "sub_pg_list1", "tenant_list", "https://example.com/list1")
		sub2 := createTestSubscription(t, "sub_pg_list2", "tenant_list", "https://example.com/list2")
		sub2.Status = SubscriptionStatusPaused
		sub3 := createTestSubscription(t, "sub_pg_list3", "tenant_other", "https://example.com/list3")

		require.NoError(t, repo.CreateSubscription(ctx, sub1))
		time.Sleep(10 * time.Millisecond)
		require.NoError(t, repo.CreateSubscription(ctx, sub2))
		time.Sleep(10 * time.Millisecond)
		require.NoError(t, repo.CreateSubscription(ctx, sub3))

		// List by tenant
		filter := &SubscriptionFilter{TenantID: "tenant_list", Limit: 100}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 2)

		// List by status
		filter = &SubscriptionFilter{Status: SubscriptionStatusPaused, Limit: 100}
		subs, err = repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(subs), 1)
		found := false
		for _, s := range subs {
			if s.ID == "sub_pg_list2" {
				found = true
				break
			}
		}
		assert.True(t, found)
	})
}

// =============================================================================
// DELIVERY TESTS WITH SKIP LOCKED
// =============================================================================

func TestPostgresRepository_DeliverySKIPLOCKED(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	// Create subscription
	sub := createTestSubscription(t, "sub_pg_skip1", "tenant_skip", "https://example.com/skip")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	// Create multiple pending deliveries
	dlv1 := createTestDelivery(t, "dlv_pg_skip1", sub.ID, sub.TenantID)
	dlv2 := createTestDelivery(t, "dlv_pg_skip2", sub.ID, sub.TenantID)
	dlv3 := createTestDelivery(t, "dlv_pg_skip3", sub.ID, sub.TenantID)

	require.NoError(t, repo.CreateDelivery(ctx, dlv1))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, repo.CreateDelivery(ctx, dlv2))
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, repo.CreateDelivery(ctx, dlv3))

	t.Run("SKIP LOCKED with concurrent access", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan string, 3)

		// Simulate 3 workers trying to get pending deliveries concurrently
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()

				// Each worker gets deliveries in its own transaction
				tx, err := repo.BeginTx(ctx)
				if err != nil {
					t.Errorf("Worker %d: failed to begin transaction: %v", workerID, err)
					return
				}
				defer tx.Rollback()

				deliveries, err := tx.GetPendingDeliveries(ctx, 1)
				if err != nil {
					t.Errorf("Worker %d: failed to get deliveries: %v", workerID, err)
					return
				}

				if len(deliveries) > 0 {
					results <- deliveries[0].ID
					// Simulate processing time
					time.Sleep(50 * time.Millisecond)
				}

				tx.Commit()
			}(i)
		}

		wg.Wait()
		close(results)

		// Collect unique delivery IDs
		uniqueDeliveries := make(map[string]bool)
		for id := range results {
			uniqueDeliveries[id] = true
		}

		// Each worker should have gotten a different delivery (SKIP LOCKED working)
		assert.GreaterOrEqual(t, len(uniqueDeliveries), 2, "SKIP LOCKED should prevent workers from getting the same delivery")
	})

	t.Run("Future retry deliveries are skipped", func(t *testing.T) {
		// Create delivery scheduled for future
		futureTime := time.Now().Add(1 * time.Hour)
		dlv4 := createTestDelivery(t, "dlv_pg_skip4", sub.ID, sub.TenantID)
		dlv4.NextRetryAt = &futureTime
		require.NoError(t, repo.CreateDelivery(ctx, dlv4))

		// Should not be returned
		deliveries, err := repo.GetPendingDeliveries(ctx, 10)
		require.NoError(t, err)

		for _, d := range deliveries {
			assert.NotEqual(t, "dlv_pg_skip4", d.ID, "Future scheduled delivery should not be returned")
		}
	})
}

// =============================================================================
// TRANSACTION TESTS
// =============================================================================

func TestPostgresRepository_Transactions(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("Commit transaction", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub := createTestSubscription(t, "sub_pg_tx1", "tenant_tx", "https://example.com/tx1")
		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Not visible outside transaction yet
		_, err = repo.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)

		// Commit
		err = tx.Commit()
		require.NoError(t, err)

		// Now visible
		retrieved, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.Equal(t, sub.ID, retrieved.ID)
	})

	t.Run("Rollback transaction", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub := createTestSubscription(t, "sub_pg_tx2", "tenant_tx", "https://example.com/tx2")
		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Rollback
		err = tx.Rollback()
		require.NoError(t, err)

		// Should not exist
		_, err = repo.GetSubscription(ctx, sub.ID)
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("Multiple operations in transaction", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		// Create subscription
		sub := createTestSubscription(t, "sub_pg_tx3", "tenant_tx", "https://example.com/tx3")
		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Create delivery
		dlv := createTestDelivery(t, "dlv_pg_tx1", sub.ID, sub.TenantID)
		err = tx.CreateDelivery(ctx, dlv)
		require.NoError(t, err)

		// Create attempt
		att := createTestDeliveryAttempt(t, "att_pg_tx1", dlv.ID, 1)
		err = tx.CreateDeliveryAttempt(ctx, att)
		require.NoError(t, err)

		// Commit all at once
		err = tx.Commit()
		require.NoError(t, err)

		// Verify all exist
		_, err = repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)

		_, err = repo.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)

		attempts, err := repo.GetDeliveryAttempts(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Len(t, attempts, 1)
	})
}

// =============================================================================
// DELIVERY ATTEMPT TESTS
// =============================================================================

func TestPostgresRepository_DeliveryAttempts(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	// Setup
	sub := createTestSubscription(t, "sub_pg_att1", "tenant_att", "https://example.com/att")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv := createTestDelivery(t, "dlv_pg_att1", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	t.Run("Create and retrieve attempts", func(t *testing.T) {
		att1 := createTestDeliveryAttempt(t, "att_pg1", dlv.ID, 1)
		att2 := createTestDeliveryAttempt(t, "att_pg2", dlv.ID, 2)
		att3 := createTestDeliveryAttempt(t, "att_pg3", dlv.ID, 3)

		require.NoError(t, repo.CreateDeliveryAttempt(ctx, att3)) // Out of order
		require.NoError(t, repo.CreateDeliveryAttempt(ctx, att1))
		require.NoError(t, repo.CreateDeliveryAttempt(ctx, att2))

		attempts, err := repo.GetDeliveryAttempts(ctx, dlv.ID)
		require.NoError(t, err)
		assert.Len(t, attempts, 3)

		// Should be sorted by attempt_number
		assert.Equal(t, 1, attempts[0].AttemptNumber)
		assert.Equal(t, 2, attempts[1].AttemptNumber)
		assert.Equal(t, 3, attempts[2].AttemptNumber)
	})

	t.Run("Duplicate attempt number prevented", func(t *testing.T) {
		att := createTestDeliveryAttempt(t, "att_pg_dup", dlv.ID, 1)
		err := repo.CreateDeliveryAttempt(ctx, att)
		assert.Error(t, err) // Unique constraint violation
	})
}

// =============================================================================
// IDEMPOTENCY TESTS
// =============================================================================

func TestPostgresRepository_Idempotency(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("Store and check idempotency key", func(t *testing.T) {
		expiresAt := time.Now().Add(1 * time.Hour)
		err := repo.StoreIdempotencyKey(ctx, "key_pg1", "sub_pg_idm1", expiresAt)
		require.NoError(t, err)

		exists, err := repo.CheckIdempotency(ctx, "key_pg1", "sub_pg_idm1")
		require.NoError(t, err)
		assert.True(t, exists)

		// Different subscription
		exists, err = repo.CheckIdempotency(ctx, "key_pg1", "sub_pg_idm2")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("Expired key", func(t *testing.T) {
		expiresAt := time.Now().Add(-1 * time.Hour) // Past
		err := repo.StoreIdempotencyKey(ctx, "key_pg_expired", "sub_pg_idm3", expiresAt)
		require.NoError(t, err)

		exists, err := repo.CheckIdempotency(ctx, "key_pg_expired", "sub_pg_idm3")
		require.NoError(t, err)
		assert.False(t, exists, "Expired key should not be found")
	})

	t.Run("Upsert behavior", func(t *testing.T) {
		expiresAt1 := time.Now().Add(1 * time.Hour)
		err := repo.StoreIdempotencyKey(ctx, "key_pg_upsert", "sub_pg_idm4", expiresAt1)
		require.NoError(t, err)

		// Update with new expiration
		expiresAt2 := time.Now().Add(2 * time.Hour)
		err = repo.StoreIdempotencyKey(ctx, "key_pg_upsert", "sub_pg_idm4", expiresAt2)
		require.NoError(t, err)

		// Should still exist
		exists, err := repo.CheckIdempotency(ctx, "key_pg_upsert", "sub_pg_idm4")
		require.NoError(t, err)
		assert.True(t, exists)
	})
}

// =============================================================================
// CIRCUIT BREAKER TESTS
// =============================================================================

func TestPostgresRepository_CircuitBreaker(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("Get default state", func(t *testing.T) {
		state, err := repo.GetCircuitBreakerState(ctx, "https://example.com/cb1")
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, state.State)
		assert.Equal(t, 0, state.FailureCount)
	})

	t.Run("Update state (upsert)", func(t *testing.T) {
		state := &CircuitBreakerState{
			Endpoint:     "https://example.com/cb2",
			State:        CircuitBreakerStateOpen,
			FailureCount: 5,
			SuccessCount: 0,
			LastFailure:  time.Now(),
			OpenedAt:     time.Now(),
			NextRetryAt:  time.Now().Add(1 * time.Minute),
		}

		err := repo.UpdateCircuitBreakerState(ctx, state)
		require.NoError(t, err)

		retrieved, err := repo.GetCircuitBreakerState(ctx, "https://example.com/cb2")
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateOpen, retrieved.State)
		assert.Equal(t, 5, retrieved.FailureCount)
	})

	t.Run("Transition states", func(t *testing.T) {
		endpoint := "https://example.com/cb3"

		// Initial: closed
		state, err := repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, state.State)

		// Transition to open
		state.State = CircuitBreakerStateOpen
		state.FailureCount = 10
		state.OpenedAt = time.Now()
		err = repo.UpdateCircuitBreakerState(ctx, state)
		require.NoError(t, err)

		// Verify open
		state, err = repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateOpen, state.State)

		// Transition to half-open
		state.State = CircuitBreakerStateHalfOpen
		state.SuccessCount = 1
		err = repo.UpdateCircuitBreakerState(ctx, state)
		require.NoError(t, err)

		// Verify half-open
		state, err = repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateHalfOpen, state.State)

		// Transition back to closed
		state.State = CircuitBreakerStateClosed
		state.FailureCount = 0
		state.SuccessCount = 3
		err = repo.UpdateCircuitBreakerState(ctx, state)
		require.NoError(t, err)

		// Verify closed
		state, err = repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, state.State)
	})
}

// =============================================================================
// CASCADE DELETE TESTS
// =============================================================================

func TestPostgresRepository_CascadeDelete(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	// Create subscription with deliveries and attempts
	sub := createTestSubscription(t, "sub_pg_cascade", "tenant_cascade", "https://example.com/cascade")
	require.NoError(t, repo.CreateSubscription(ctx, sub))

	dlv := createTestDelivery(t, "dlv_pg_cascade", sub.ID, sub.TenantID)
	require.NoError(t, repo.CreateDelivery(ctx, dlv))

	att := createTestDeliveryAttempt(t, "att_pg_cascade", dlv.ID, 1)
	require.NoError(t, repo.CreateDeliveryAttempt(ctx, att))

	// Delete subscription
	err := repo.DeleteSubscription(ctx, sub.ID)
	require.NoError(t, err)

	// Verify cascade delete
	_, err = repo.GetDelivery(ctx, dlv.ID)
	assert.ErrorIs(t, err, ErrDeliveryNotFound, "Delivery should be cascade deleted")

	attempts, err := repo.GetDeliveryAttempts(ctx, dlv.ID)
	require.NoError(t, err)
	assert.Empty(t, attempts, "Attempts should be cascade deleted")
}

// =============================================================================
// HEALTH AND MAINTENANCE TESTS
// =============================================================================

func TestPostgresRepository_Health(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("Ping", func(t *testing.T) {
		err := repo.Ping(ctx)
		assert.NoError(t, err)
	})

	t.Run("Close", func(t *testing.T) {
		// Create a separate connection to test close
		connStr := getTestConnectionString()
		repo2, err := NewPostgresRepository(connStr)
		require.NoError(t, err)

		err = repo2.Close()
		assert.NoError(t, err)

		// Should not be able to ping after close
		err = repo2.Ping(ctx)
		assert.Error(t, err)
	})
}

// =============================================================================
// ERROR PATH TESTS
// =============================================================================

func TestPostgresRepository_ErrorPaths(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("CreateSubscription - duplicate tenant+URL", func(t *testing.T) {
		sub := createTestSubscription(t, "sub_err1", "tenant_err", "https://example.com/err1")
		err := repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Try to create duplicate with same tenant_id + url
		sub2 := createTestSubscription(t, "sub_err2", "tenant_err", "https://example.com/err1")
		err = repo.CreateSubscription(ctx, sub2)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrDuplicateSubscription)
	})

	t.Run("UpdateSubscription - not found", func(t *testing.T) {
		sub := createTestSubscription(t, "sub_nonexistent", "tenant_err", "https://example.com/notfound")
		err := repo.UpdateSubscription(ctx, sub)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("DeleteSubscription - not found", func(t *testing.T) {
		err := repo.DeleteSubscription(ctx, "sub_nonexistent")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("GetDelivery - not found", func(t *testing.T) {
		_, err := repo.GetDelivery(ctx, "dlv_nonexistent")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrDeliveryNotFound)
	})

	t.Run("UpdateDelivery - not found", func(t *testing.T) {
		dlv := createTestDelivery(t, "dlv_nonexistent", "sub_test", "tenant_test")
		err := repo.UpdateDelivery(ctx, dlv)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrDeliveryNotFound)
	})

	t.Run("MoveToDeadLetter - not found", func(t *testing.T) {
		err := repo.MoveToDeadLetter(ctx, "dlv_nonexistent", "test reason")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrDeliveryNotFound)
	})

	t.Run("CreateDeliveryAttempt - duplicate attempt number", func(t *testing.T) {
		// Setup: create subscription and delivery
		sub := createTestSubscription(t, "sub_err_att", "tenant_err", "https://example.com/err_att")
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		dlv := createTestDelivery(t, "dlv_err_att", sub.ID, sub.TenantID)
		require.NoError(t, repo.CreateDelivery(ctx, dlv))

		// Create first attempt
		att1 := createTestDeliveryAttempt(t, "att_err1", dlv.ID, 1)
		err := repo.CreateDeliveryAttempt(ctx, att1)
		require.NoError(t, err)

		// Try to create duplicate attempt with same number
		att2 := createTestDeliveryAttempt(t, "att_err2", dlv.ID, 1)
		err = repo.CreateDeliveryAttempt(ctx, att2)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unique")
	})

	t.Run("ListSubscriptions - empty filter", func(t *testing.T) {
		filter := &SubscriptionFilter{Limit: 100}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.NotNil(t, subs)
		// Should return what exists
	})

	t.Run("ListSubscriptions - no matches", func(t *testing.T) {
		filter := &SubscriptionFilter{
			TenantID: "tenant_nonexistent",
			Limit:    100,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Empty(t, subs)
	})

	t.Run("GetDeliveryAttempts - empty results", func(t *testing.T) {
		attempts, err := repo.GetDeliveryAttempts(ctx, "dlv_nonexistent")
		require.NoError(t, err)
		assert.Empty(t, attempts)
	})

	t.Run("CheckIdempotency - expired key", func(t *testing.T) {
		expiresAt := time.Now().Add(-1 * time.Hour) // Expired
		err := repo.StoreIdempotencyKey(ctx, "key_expired", "sub_test", expiresAt)
		require.NoError(t, err)

		exists, err := repo.CheckIdempotency(ctx, "key_expired", "sub_test")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("GetCircuitBreakerState - default for new endpoint", func(t *testing.T) {
		state, err := repo.GetCircuitBreakerState(ctx, "https://newendpoint.com/webhook")
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, state.State)
		assert.Equal(t, 0, state.FailureCount)
	})
}

func TestPostgresRepository_TransactionErrors(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("Operations after commit should fail", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		// Commit transaction
		err = tx.Commit()
		require.NoError(t, err)

		// Try to create subscription after commit
		sub := createTestSubscription(t, "sub_after_commit", "tenant_err", "https://example.com/after")
		err = tx.CreateSubscription(ctx, sub)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sql: transaction has already been committed")
	})

	t.Run("Operations after rollback should fail", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		// Rollback transaction
		err = tx.Rollback()
		require.NoError(t, err)

		// Try to create subscription after rollback
		sub := createTestSubscription(t, "sub_after_rollback", "tenant_err", "https://example.com/after2")
		err = tx.CreateSubscription(ctx, sub)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sql: transaction has already been committed")
	})

	t.Run("Double commit is safe", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Second commit should not panic
		err = tx.Commit()
		assert.NoError(t, err)
	})

	t.Run("Double rollback is safe", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Second rollback should not panic
		err = tx.Rollback()
		assert.NoError(t, err)
	})
}

func TestPostgresRepository_ConcurrentUpdates(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("Concurrent subscription updates", func(t *testing.T) {
		// Create subscription
		sub := createTestSubscription(t, "sub_concurrent", "tenant_conc", "https://example.com/conc")
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		// Update concurrently from multiple goroutines
		var wg sync.WaitGroup
		errorCount := 0
		mu := sync.Mutex{}

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(iteration int) {
				defer wg.Done()

				// Get current subscription
				current, err := repo.GetSubscription(ctx, sub.ID)
				if err != nil {
					mu.Lock()
					errorCount++
					mu.Unlock()
					return
				}

				// Update status
				if iteration%2 == 0 {
					current.Status = SubscriptionStatusActive
				} else {
					current.Status = SubscriptionStatusPaused
				}

				err = repo.UpdateSubscription(ctx, current)
				if err != nil {
					mu.Lock()
					errorCount++
					mu.Unlock()
				}
			}(i)
		}

		wg.Wait()

		// Some errors are acceptable due to concurrent updates, but not all
		assert.Less(t, errorCount, 5, "Too many errors in concurrent updates")

		// Subscription should still be retrievable
		_, err := repo.GetSubscription(ctx, sub.ID)
		assert.NoError(t, err)
	})

	t.Run("Concurrent delivery updates", func(t *testing.T) {
		// Setup
		sub := createTestSubscription(t, "sub_dlv_conc", "tenant_conc", "https://example.com/dlv_conc")
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		dlv := createTestDelivery(t, "dlv_concurrent", sub.ID, sub.TenantID)
		require.NoError(t, repo.CreateDelivery(ctx, dlv))

		// Update concurrently
		var wg sync.WaitGroup
		successCount := 0
		mu := sync.Mutex{}

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(iteration int) {
				defer wg.Done()

				current, err := repo.GetDelivery(ctx, dlv.ID)
				if err != nil {
					return
				}

				current.AttemptCount = iteration
				err = repo.UpdateDelivery(ctx, current)
				if err == nil {
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			}(i)
		}

		wg.Wait()

		// At least some updates should succeed
		assert.Greater(t, successCount, 0, "At least some concurrent updates should succeed")
	})
}

func TestPostgresRepository_EdgeCases(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("Empty event types filter", func(t *testing.T) {
		filter := &SubscriptionFilter{
			EventTypes: []string{},
			Limit:      100,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.NotNil(t, subs)
	})

	t.Run("Very large offset", func(t *testing.T) {
		filter := &SubscriptionFilter{
			Offset: 999999,
			Limit:  100,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Empty(t, subs)
	})

	t.Run("Zero limit", func(t *testing.T) {
		filter := &SubscriptionFilter{
			Limit: 0,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.NotNil(t, subs)
	})

	t.Run("Subscription with nil metadata", func(t *testing.T) {
		sub := createTestSubscription(t, "sub_nil_meta", "tenant_edge", "https://example.com/nil")
		sub.Metadata = nil
		err := repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		retrieved, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.NotNil(t, retrieved)
	})

	t.Run("Subscription with nil headers", func(t *testing.T) {
		sub := createTestSubscription(t, "sub_nil_headers", "tenant_edge", "https://example.com/nil2")
		sub.Headers = nil
		err := repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		retrieved, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		assert.NotNil(t, retrieved)
	})

	t.Run("Delivery with nil payload", func(t *testing.T) {
		sub := createTestSubscription(t, "sub_nil_payload", "tenant_edge", "https://example.com/nil3")
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		dlv := createTestDelivery(t, "dlv_nil_payload", sub.ID, sub.TenantID)
		dlv.Payload = nil
		err := repo.CreateDelivery(ctx, dlv)
		require.NoError(t, err)

		retrieved, err := repo.GetDelivery(ctx, dlv.ID)
		require.NoError(t, err)
		assert.NotNil(t, retrieved)
	})

	t.Run("GetPendingDeliveries with limit 0", func(t *testing.T) {
		deliveries, err := repo.GetPendingDeliveries(ctx, 0)
		require.NoError(t, err)
		assert.Empty(t, deliveries)
	})

	t.Run("GetPendingDeliveries with very large limit", func(t *testing.T) {
		deliveries, err := repo.GetPendingDeliveries(ctx, 999999)
		require.NoError(t, err)
		assert.NotNil(t, deliveries)
	})

	t.Run("Circuit breaker with very old timestamps", func(t *testing.T) {
		state := &CircuitBreakerState{
			Endpoint:     "https://old.com/webhook",
			State:        CircuitBreakerStateOpen,
			FailureCount: 5,
			SuccessCount: 0,
			LastFailure:  time.Now().Add(-24 * time.Hour),
			OpenedAt:     time.Now().Add(-24 * time.Hour),
			NextRetryAt:  time.Now().Add(-1 * time.Hour), // In the past
		}
		err := repo.UpdateCircuitBreakerState(ctx, state)
		require.NoError(t, err)

		retrieved, err := repo.GetCircuitBreakerState(ctx, "https://old.com/webhook")
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateOpen, retrieved.State)
	})

	t.Run("Multiple event type filters", func(t *testing.T) {
		// Create subscription with multiple event types
		sub := createTestSubscription(t, "sub_multi_events", "tenant_edge", "https://example.com/multi")
		sub.EventTypes = []string{"user.created", "user.updated", "user.deleted"}
		require.NoError(t, repo.CreateSubscription(ctx, sub))

		// Filter by multiple event types
		filter := &SubscriptionFilter{
			EventTypes: []string{"user.created", "user.updated"},
			Limit:      100,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.NotEmpty(t, subs)

		// Verify the subscription is in results
		found := false
		for _, s := range subs {
			if s.ID == sub.ID {
				found = true
				break
			}
		}
		assert.True(t, found, "Subscription with matching event types should be in results")
	})
}

func TestPostgresRepository_ComplexFilters(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	// Create test data
	sub1 := createTestSubscription(t, "sub_filter1", "tenant_filter", "https://example.com/filter1")
	sub1.Status = SubscriptionStatusActive
	sub1.EventTypes = []string{"user.created", "user.updated"}
	require.NoError(t, repo.CreateSubscription(ctx, sub1))

	sub2 := createTestSubscription(t, "sub_filter2", "tenant_filter", "https://example.com/filter2")
	sub2.Status = SubscriptionStatusPaused
	sub2.EventTypes = []string{"user.deleted"}
	require.NoError(t, repo.CreateSubscription(ctx, sub2))

	sub3 := createTestSubscription(t, "sub_filter3", "tenant_other", "https://example.com/filter3")
	sub3.Status = SubscriptionStatusActive
	sub3.EventTypes = []string{"user.created"}
	require.NoError(t, repo.CreateSubscription(ctx, sub3))

	t.Run("Filter by tenant and status", func(t *testing.T) {
		filter := &SubscriptionFilter{
			TenantID: "tenant_filter",
			Status:   SubscriptionStatusActive,
			Limit:    100,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 1)
		assert.Equal(t, "sub_filter1", subs[0].ID)
	})

	t.Run("Filter by tenant and event type", func(t *testing.T) {
		filter := &SubscriptionFilter{
			TenantID:   "tenant_filter",
			EventTypes: []string{"user.created"},
			Limit:      100,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 1)
		assert.Equal(t, "sub_filter1", subs[0].ID)
	})

	t.Run("Filter by status and event type", func(t *testing.T) {
		filter := &SubscriptionFilter{
			Status:     SubscriptionStatusActive,
			EventTypes: []string{"user.created"},
			Limit:      100,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(subs), 2) // sub_filter1 and sub_filter3
	})

	t.Run("All filters combined", func(t *testing.T) {
		filter := &SubscriptionFilter{
			TenantID:   "tenant_filter",
			Status:     SubscriptionStatusActive,
			EventTypes: []string{"user.created"},
			Limit:      100,
		}
		subs, err := repo.ListSubscriptions(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, subs, 1)
		assert.Equal(t, "sub_filter1", subs[0].ID)
	})
}

// =============================================================================
// MIGRATION VERIFICATION TESTS
// =============================================================================

func TestPostgresRepository_SchemaVerification(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	if repo == nil {
		return // Skipped
	}
	defer cleanup()

	ctx := context.Background()

	t.Run("All tables exist", func(t *testing.T) {
		tables := []string{
			"subscriptions",
			"deliveries",
			"delivery_attempts",
			"idempotency_store",
			"circuit_breaker_state",
		}

		for _, table := range tables {
			var exists bool
			query := `SELECT EXISTS (
				SELECT FROM information_schema.tables
				WHERE table_schema = 'public'
				AND table_name = $1
			)`
			err := repo.db.QueryRowContext(ctx, query, table).Scan(&exists)
			require.NoError(t, err)
			assert.True(t, exists, "Table %s should exist", table)
		}
	})

	t.Run("Critical indexes exist", func(t *testing.T) {
		indexes := []string{
			"idx_deliveries_pending_queue", // SKIP LOCKED optimization
			"idx_subscriptions_tenant_url", // Unique constraint
			"idx_attempts_delivery_number", // Unique constraint
		}

		for _, index := range indexes {
			var exists bool
			query := `SELECT EXISTS (
				SELECT FROM pg_indexes
				WHERE schemaname = 'public'
				AND indexname = $1
			)`
			err := repo.db.QueryRowContext(ctx, query, index).Scan(&exists)
			require.NoError(t, err)
			assert.True(t, exists, "Index %s should exist", index)
		}
	})

	t.Run("Triggers exist", func(t *testing.T) {
		triggers := []string{
			"trg_subscriptions_updated_at",
			"trg_circuit_breaker_updated_at",
		}

		for _, trigger := range triggers {
			var exists bool
			query := `SELECT EXISTS (
				SELECT FROM pg_trigger
				WHERE tgname = $1
			)`
			err := repo.db.QueryRowContext(ctx, query, trigger).Scan(&exists)
			require.NoError(t, err)
			assert.True(t, exists, "Trigger %s should exist", trigger)
		}
	})
}
