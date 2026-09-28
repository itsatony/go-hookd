//go:build integration
// +build integration

// Package hookd provides webhook management functionality.
//
// This file contains integration tests for multi-prefix isolation.
// These tests verify that multiple services using different prefixes
// can share the same PostgreSQL database without data leakage.
//
// Run with: go test -tags=integration -run TestMultiPrefix
//
// Excellence. Always.
package hookd

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// MULTI-PREFIX ISOLATION TESTS
// =============================================================================

// TestMultiPrefix_Isolation verifies complete data isolation between prefixes.
func TestMultiPrefix_Isolation(t *testing.T) {
	// Setup test container
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create two schema configs with different prefixes
	schemaConfigA, err := NewSchemaConfig("servicea")
	require.NoError(t, err)
	schemaConfigB, err := NewSchemaConfig("serviceb")
	require.NoError(t, err)

	// Setup schemas
	managerA := NewSchemaManager(pgContainer.db, schemaConfigA)
	managerB := NewSchemaManager(pgContainer.db, schemaConfigB)

	err = managerA.EnsureSchema(ctx)
	require.NoError(t, err)
	err = managerB.EnsureSchema(ctx)
	require.NoError(t, err)

	// Create repositories for each service
	repoA, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("servicea"))
	require.NoError(t, err)
	defer repoA.Close()

	repoB, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("serviceb"))
	require.NoError(t, err)
	defer repoB.Close()

	t.Run("subscriptions_are_isolated", func(t *testing.T) {
		// Create subscription in service A
		subA := &Subscription{
			ID:         "sub_servicea_001",
			TenantID:   "tenant_a",
			URL:        "https://a.example.com/webhook",
			Secret:     "secret_a",
			EventTypes: []string{"order.created"},
			Headers:    map[string]string{},
			Metadata:   map[string]interface{}{},
			Status:     SubscriptionStatusActive,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		err := repoA.CreateSubscription(ctx, subA)
		require.NoError(t, err)

		// Create subscription in service B with same tenant
		subB := &Subscription{
			ID:         "sub_serviceb_001",
			TenantID:   "tenant_a", // Same tenant as A
			URL:        "https://b.example.com/webhook",
			Secret:     "secret_b",
			EventTypes: []string{"user.created"},
			Headers:    map[string]string{},
			Metadata:   map[string]interface{}{},
			Status:     SubscriptionStatusActive,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		err = repoB.CreateSubscription(ctx, subB)
		require.NoError(t, err)

		// Verify A can only see A's subscription
		retrievedA, err := repoA.GetSubscription(ctx, "sub_servicea_001")
		require.NoError(t, err)
		assert.Equal(t, subA.URL, retrievedA.URL)

		// A should NOT see B's subscription
		_, err = repoA.GetSubscription(ctx, "sub_serviceb_001")
		assert.Error(t, err, "service A should not see service B's subscription")

		// Verify B can only see B's subscription
		retrievedB, err := repoB.GetSubscription(ctx, "sub_serviceb_001")
		require.NoError(t, err)
		assert.Equal(t, subB.URL, retrievedB.URL)

		// B should NOT see A's subscription
		_, err = repoB.GetSubscription(ctx, "sub_servicea_001")
		assert.Error(t, err, "service B should not see service A's subscription")

		// List by tenant should be isolated
		listA, err := repoA.ListSubscriptions(ctx, &SubscriptionFilter{TenantID: "tenant_a"})
		require.NoError(t, err)
		assert.Len(t, listA, 1, "service A should only see 1 subscription for tenant_a")
		assert.Equal(t, "sub_servicea_001", listA[0].ID)

		listB, err := repoB.ListSubscriptions(ctx, &SubscriptionFilter{TenantID: "tenant_a"})
		require.NoError(t, err)
		assert.Len(t, listB, 1, "service B should only see 1 subscription for tenant_a")
		assert.Equal(t, "sub_serviceb_001", listB[0].ID)
	})

	t.Run("deliveries_are_isolated", func(t *testing.T) {
		// Get existing subscriptions
		subA, err := repoA.GetSubscription(ctx, "sub_servicea_001")
		require.NoError(t, err)
		subB, err := repoB.GetSubscription(ctx, "sub_serviceb_001")
		require.NoError(t, err)

		// Create delivery in service A
		deliveryA := &Delivery{
			ID:             "dlv_servicea_001",
			SubscriptionID: subA.ID,
			TenantID:       subA.TenantID,
			EventType:      "order.created",
			Payload:        map[string]interface{}{"order_id": "123"},
			Status:         DeliveryStatusPending,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		err = repoA.CreateDelivery(ctx, deliveryA)
		require.NoError(t, err)

		// Create delivery in service B
		deliveryB := &Delivery{
			ID:             "dlv_serviceb_001",
			SubscriptionID: subB.ID,
			TenantID:       subB.TenantID,
			EventType:      "user.created",
			Payload:        map[string]interface{}{"user_id": "456"},
			Status:         DeliveryStatusPending,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		err = repoB.CreateDelivery(ctx, deliveryB)
		require.NoError(t, err)

		// Verify isolation
		retrievedA, err := repoA.GetDelivery(ctx, "dlv_servicea_001")
		require.NoError(t, err)
		assert.Equal(t, "order.created", retrievedA.EventType)

		_, err = repoA.GetDelivery(ctx, "dlv_serviceb_001")
		assert.Error(t, err, "service A should not see service B's delivery")

		retrievedB, err := repoB.GetDelivery(ctx, "dlv_serviceb_001")
		require.NoError(t, err)
		assert.Equal(t, "user.created", retrievedB.EventType)

		_, err = repoB.GetDelivery(ctx, "dlv_servicea_001")
		assert.Error(t, err, "service B should not see service A's delivery")
	})

	t.Run("idempotency_keys_are_isolated", func(t *testing.T) {
		// Same idempotency key can be used by both services
		idempKey := "shared_idemp_key"

		err := repoA.SetIdempotencyKey(ctx, idempKey, "dlv_servicea_002", time.Hour)
		require.NoError(t, err)

		err = repoB.SetIdempotencyKey(ctx, idempKey, "dlv_serviceb_002", time.Hour)
		require.NoError(t, err)

		// Each should return their own delivery ID
		existsA, deliveryIDA, err := repoA.CheckIdempotencyKey(ctx, idempKey)
		require.NoError(t, err)
		assert.True(t, existsA)
		assert.Equal(t, "dlv_servicea_002", deliveryIDA)

		existsB, deliveryIDB, err := repoB.CheckIdempotencyKey(ctx, idempKey)
		require.NoError(t, err)
		assert.True(t, existsB)
		assert.Equal(t, "dlv_serviceb_002", deliveryIDB)
	})

	t.Run("circuit_breaker_states_are_isolated", func(t *testing.T) {
		endpoint := "https://shared.example.com/webhook"

		// Service A opens circuit
		stateA := &CircuitBreakerState{
			EndpointURL:     endpoint,
			State:           CircuitBreakerStateOpen,
			FailureCount:    10,
			SuccessCount:    0,
			ConsecutiveFail: 5,
			LastFailure:     time.Now(),
			OpenedAt:        time.Now(),
		}
		err := repoA.SaveCircuitBreakerState(ctx, stateA)
		require.NoError(t, err)

		// Service B has closed circuit for same endpoint
		stateB := &CircuitBreakerState{
			EndpointURL:     endpoint,
			State:           CircuitBreakerStateClosed,
			FailureCount:    0,
			SuccessCount:    100,
			ConsecutiveFail: 0,
		}
		err = repoB.SaveCircuitBreakerState(ctx, stateB)
		require.NoError(t, err)

		// Verify isolation
		retrievedA, err := repoA.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateOpen, retrievedA.State)

		retrievedB, err := repoB.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		assert.Equal(t, CircuitBreakerStateClosed, retrievedB.State)
	})

	// Cleanup
	managerA.DropSchema(ctx)
	managerB.DropSchema(ctx)
}

// TestMultiPrefix_ConcurrentOperations tests parallel CRUD across prefixes.
func TestMultiPrefix_ConcurrentOperations(t *testing.T) {
	// Setup test container
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create schemas for 3 services
	prefixes := []string{"svc1", "svc2", "svc3"}
	repos := make([]*PostgresRepository, len(prefixes))

	for i, prefix := range prefixes {
		schemaConfig, err := NewSchemaConfig(prefix)
		require.NoError(t, err)

		manager := NewSchemaManager(pgContainer.db, schemaConfig)
		err = manager.EnsureSchema(ctx)
		require.NoError(t, err)
		defer manager.DropSchema(ctx)

		repos[i], err = NewPostgresRepository(pgContainer.connStr, WithTablePrefix(prefix))
		require.NoError(t, err)
		defer repos[i].Close()
	}

	t.Run("concurrent_subscription_creation", func(t *testing.T) {
		var wg sync.WaitGroup
		errCh := make(chan error, len(prefixes)*10)

		for i, repo := range repos {
			for j := 0; j < 10; j++ {
				wg.Add(1)
				go func(repoIdx, subIdx int, r *PostgresRepository) {
					defer wg.Done()

					sub := &Subscription{
						ID:         generateTestID("sub", repoIdx, subIdx),
						TenantID:   "concurrent_test",
						URL:        "https://example.com/webhook",
						Secret:     "secret",
						EventTypes: []string{"test.event"},
						Headers:    map[string]string{},
						Metadata:   map[string]interface{}{},
						Status:     SubscriptionStatusActive,
						CreatedAt:  time.Now(),
						UpdatedAt:  time.Now(),
					}

					if err := r.CreateSubscription(ctx, sub); err != nil {
						errCh <- err
					}
				}(i, j, repo)
			}
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			t.Errorf("concurrent creation error: %v", err)
		}

		// Verify each repo has exactly 10 subscriptions
		for i, repo := range repos {
			list, err := repo.ListSubscriptions(ctx, &SubscriptionFilter{TenantID: "concurrent_test"})
			require.NoError(t, err)
			assert.Len(t, list, 10, "repo %d should have 10 subscriptions", i)
		}
	})

	t.Run("concurrent_deliveries_with_queue_polling", func(t *testing.T) {
		var wg sync.WaitGroup
		errCh := make(chan error, len(prefixes)*10)

		// Create deliveries concurrently
		for i, repo := range repos {
			for j := 0; j < 10; j++ {
				wg.Add(1)
				go func(repoIdx, dlvIdx int, r *PostgresRepository) {
					defer wg.Done()

					subID := generateTestID("sub", repoIdx, 0)
					dlv := &Delivery{
						ID:             generateTestID("dlv", repoIdx, dlvIdx),
						SubscriptionID: subID,
						TenantID:       "concurrent_test",
						EventType:      "test.event",
						Payload:        map[string]interface{}{"idx": dlvIdx},
						Status:         DeliveryStatusPending,
						ScheduledFor:   time.Now(),
						CreatedAt:      time.Now(),
						UpdatedAt:      time.Now(),
					}

					if err := r.CreateDelivery(ctx, dlv); err != nil {
						errCh <- err
					}
				}(i, j, repo)
			}
		}

		wg.Wait()
		close(errCh)

		for err := range errCh {
			t.Errorf("concurrent delivery creation error: %v", err)
		}

		// Poll queues - each should get their own deliveries
		for i, repo := range repos {
			deliveries, err := repo.ClaimPendingDeliveries(ctx, 20, testClaimLease)
			require.NoError(t, err)
			assert.Len(t, deliveries, 10, "repo %d should have 10 pending deliveries", i)

			// Verify all deliveries belong to this service's prefix
			for _, dlv := range deliveries {
				assert.Contains(t, dlv.ID, generatePrefix("dlv", i), "delivery should belong to service %d", i)
			}
		}
	})
}

// TestMultiPrefix_NoDataLeakage performs comprehensive leakage tests.
func TestMultiPrefix_NoDataLeakage(t *testing.T) {
	// Setup test container
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Setup two services
	schemaConfigA, _ := NewSchemaConfig("leaktest_a")
	schemaConfigB, _ := NewSchemaConfig("leaktest_b")

	managerA := NewSchemaManager(pgContainer.db, schemaConfigA)
	managerB := NewSchemaManager(pgContainer.db, schemaConfigB)

	err := managerA.EnsureSchema(ctx)
	require.NoError(t, err)
	defer managerA.DropSchema(ctx)

	err = managerB.EnsureSchema(ctx)
	require.NoError(t, err)
	defer managerB.DropSchema(ctx)

	repoA, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("leaktest_a"))
	require.NoError(t, err)
	defer repoA.Close()

	repoB, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("leaktest_b"))
	require.NoError(t, err)
	defer repoB.Close()

	// Seed data in service A
	for i := 0; i < 100; i++ {
		sub := &Subscription{
			ID:         generateSeededID("sub", i),
			TenantID:   "leak_tenant",
			URL:        "https://a.example.com/webhook",
			Secret:     "secret_a",
			EventTypes: []string{"event.a"},
			Headers:    map[string]string{},
			Metadata:   map[string]interface{}{},
			Status:     SubscriptionStatusActive,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		err := repoA.CreateSubscription(ctx, sub)
		require.NoError(t, err)
	}

	t.Run("list_all_shows_no_leakage", func(t *testing.T) {
		// Service B should see no subscriptions even with no filter
		listB, err := repoB.ListSubscriptions(ctx, &SubscriptionFilter{})
		require.NoError(t, err)
		assert.Len(t, listB, 0, "service B should see 0 subscriptions")

		// Service A should see all 100
		listA, err := repoA.ListSubscriptions(ctx, &SubscriptionFilter{})
		require.NoError(t, err)
		assert.Len(t, listA, 100, "service A should see 100 subscriptions")
	})

	t.Run("get_by_id_shows_no_leakage", func(t *testing.T) {
		// Try to get each A subscription from B
		for i := 0; i < 10; i++ {
			id := generateSeededID("sub", i)
			_, err := repoB.GetSubscription(ctx, id)
			assert.Error(t, err, "service B should not be able to get A's subscription %s", id)
		}
	})

	t.Run("update_shows_no_leakage", func(t *testing.T) {
		// Try to update A's subscription from B
		_, err := repoB.UpdateSubscription(ctx, generateSeededID("sub", 0), &SubscriptionUpdate{
			URL: stringPtr("https://hacked.example.com"),
		})
		assert.Error(t, err, "service B should not be able to update A's subscription")

		// Verify A's subscription is unchanged
		sub, err := repoA.GetSubscription(ctx, generateSeededID("sub", 0))
		require.NoError(t, err)
		assert.Equal(t, "https://a.example.com/webhook", sub.URL, "subscription should be unchanged")
	})

	t.Run("delete_shows_no_leakage", func(t *testing.T) {
		// Try to delete A's subscription from B
		err := repoB.DeleteSubscription(ctx, generateSeededID("sub", 0))
		assert.Error(t, err, "service B should not be able to delete A's subscription")

		// Verify A's subscription still exists
		_, err = repoA.GetSubscription(ctx, generateSeededID("sub", 0))
		assert.NoError(t, err, "A's subscription should still exist")
	})
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

func generateTestID(prefix string, repoIdx, subIdx int) string {
	return generatePrefix(prefix, repoIdx) + "_" + string(rune('0'+subIdx))
}

func generatePrefix(prefix string, repoIdx int) string {
	return prefix + "_svc" + string(rune('1'+repoIdx))
}

func generateSeededID(prefix string, idx int) string {
	return prefix + "_seed_" + string(rune('a'+idx%26)) + string(rune('0'+idx/26))
}

func stringPtr(s string) *string {
	return &s
}
