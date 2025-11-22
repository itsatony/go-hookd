//go:build integration
// +build integration

package hookd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// =============================================================================
// TRANSACTION CONTROL TESTS
// =============================================================================

func TestPostgresRepositoryTx_Commit_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("happy_path_commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub := &Subscription{
			ID:         "sub_commit_test",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/webhook",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify persisted
		fetchedSub, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		require.Equal(t, sub.ID, fetchedSub.ID)
	})

	t.Run("double_commit_safe", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub := &Subscription{
			ID:         "sub_double_commit",
			TenantID:   "tenant_tx",
			URL:        "https://example2.com/webhook",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Second commit should be safe (no-op)
		err = tx.Commit()
		require.NoError(t, err)
	})
}

func TestPostgresRepositoryTx_Rollback_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("happy_path_rollback", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub := &Subscription{
			ID:         "sub_rollback_test",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/webhook",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT persisted
		_, err = repo.GetSubscription(ctx, sub.ID)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("rollback_after_commit_safe", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub := &Subscription{
			ID:         "sub_rollback_after_commit",
			TenantID:   "tenant_tx",
			URL:        "https://example3.com/webhook",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Rollback after commit should be safe (no-op)
		err = tx.Rollback()
		require.NoError(t, err)

		// Verify still persisted
		fetchedSub, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		require.Equal(t, sub.ID, fetchedSub.ID)
	})
}

// =============================================================================
// SUBSCRIPTION OPERATIONS IN TRANSACTION TESTS
// =============================================================================

func TestPostgresRepositoryTx_CreateSubscription_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("create_and_commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub := &Subscription{
			ID:         "sub_tx_create_1",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/webhook1",
			Secret:     "secret",
			EventTypes: []string{"user.created", "user.updated"},
			Status:     SubscriptionStatusActive,
			RetryPolicy: &RetryPolicy{
				MaxAttempts:    10,
				InitialBackoff: 1 * time.Second,
				MaxBackoff:     1 * time.Hour,
				BackoffFactor:  2.0,
			},
			Headers:  map[string]string{"X-Custom": "value"},
			Metadata: map[string]interface{}{"key": "value"},
		}

		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify persisted
		fetchedSub, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		require.Equal(t, sub.ID, fetchedSub.ID)
		require.Equal(t, sub.TenantID, fetchedSub.TenantID)
		require.Equal(t, sub.URL, fetchedSub.URL)
	})

	t.Run("create_and_rollback", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub := &Subscription{
			ID:         "sub_tx_rollback_1",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/webhook2",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT persisted
		_, err = repo.GetSubscription(ctx, sub.ID)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("duplicate_constraint_violation", func(t *testing.T) {
		// Create initial subscription
		sub := &Subscription{
			ID:         "sub_tx_duplicate",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/duplicate",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}
		err = repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Try to create duplicate in transaction
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		dupSub := &Subscription{
			ID:         "sub_tx_duplicate_2",
			TenantID:   sub.TenantID,
			URL:        sub.URL, // Same tenant+URL
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.CreateSubscription(ctx, dupSub)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrDuplicateSubscription)
	})
}

func TestPostgresRepositoryTx_GetSubscription_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("get_existing_subscription", func(t *testing.T) {
		// Create subscription
		sub := &Subscription{
			ID:         "sub_tx_get_1",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/get1",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}
		err = repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Get in transaction
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		fetchedSub, err := tx.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		require.Equal(t, sub.ID, fetchedSub.ID)
	})

	t.Run("get_non_existent", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		_, err = tx.GetSubscription(ctx, "sub_non_existent")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("read_uncommitted_create", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		sub := &Subscription{
			ID:         "sub_tx_uncommitted",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/uncommitted",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Read within same transaction
		fetchedSub, err := tx.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		require.Equal(t, sub.ID, fetchedSub.ID)
	})
}

func TestPostgresRepositoryTx_GetSubscriptionByTenantAndURL_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("get_by_tenant_url", func(t *testing.T) {
		sub := &Subscription{
			ID:         "sub_tx_tenant_url",
			TenantID:   "tenant_specific",
			URL:        "https://example.com/tenant-url",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}
		err = repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		fetchedSub, err := tx.GetSubscriptionByTenantAndURL(ctx, sub.TenantID, sub.URL)
		require.NoError(t, err)
		require.Equal(t, sub.ID, fetchedSub.ID)
	})

	t.Run("not_found", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		_, err = tx.GetSubscriptionByTenantAndURL(ctx, "non_existent", "https://nope.com")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
	})
}

func TestPostgresRepositoryTx_UpdateSubscription_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("update_and_commit", func(t *testing.T) {
		// Create initial subscription
		sub := &Subscription{
			ID:         "sub_tx_update_1",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/update1",
			Secret:     "secret",
			EventTypes: []string{"event.original"},
			Status:     SubscriptionStatusActive,
		}
		err = repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Update in transaction
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub.URL = "https://updated.com/webhook"
		sub.EventTypes = []string{"event.updated"}
		sub.Status = SubscriptionStatusPaused

		err = tx.UpdateSubscription(ctx, sub)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify changes persisted
		updated, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		require.Equal(t, "https://updated.com/webhook", updated.URL)
		require.Equal(t, SubscriptionStatusPaused, updated.Status)
	})

	t.Run("update_and_rollback", func(t *testing.T) {
		// Create initial subscription
		sub := &Subscription{
			ID:         "sub_tx_update_rollback",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/update-rollback",
			Secret:     "secret",
			EventTypes: []string{"event.original"},
			Status:     SubscriptionStatusActive,
		}
		err = repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// Update in transaction but rollback
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		sub.Status = SubscriptionStatusPaused
		err = tx.UpdateSubscription(ctx, sub)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify changes NOT persisted
		fetched, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		require.Equal(t, SubscriptionStatusActive, fetched.Status)
	})

	t.Run("update_non_existent", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		sub := &Subscription{
			ID:         "sub_does_not_exist",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/nope",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.UpdateSubscription(ctx, sub)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
	})
}

func TestPostgresRepositoryTx_DeleteSubscription_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("delete_and_commit", func(t *testing.T) {
		sub := &Subscription{
			ID:         "sub_tx_delete_1",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/delete1",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}
		err = repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.DeleteSubscription(ctx, sub.ID)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify deleted
		_, err = repo.GetSubscription(ctx, sub.ID)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
	})

	t.Run("delete_and_rollback", func(t *testing.T) {
		sub := &Subscription{
			ID:         "sub_tx_delete_rollback",
			TenantID:   "tenant_tx",
			URL:        "https://example.com/delete-rollback",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}
		err = repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.DeleteSubscription(ctx, sub.ID)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT deleted
		fetched, err := repo.GetSubscription(ctx, sub.ID)
		require.NoError(t, err)
		require.Equal(t, sub.ID, fetched.ID)
	})

	t.Run("delete_non_existent", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		err = tx.DeleteSubscription(ctx, "sub_does_not_exist")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSubscriptionNotFound)
	})
}

func TestPostgresRepositoryTx_ListSubscriptions_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("list_with_filters", func(t *testing.T) {
		// Create multiple subscriptions
		for i := 0; i < 3; i++ {
			sub := &Subscription{
				ID:         fmt.Sprintf("sub_tx_list_%d", i),
				TenantID:   "tenant_list_tx",
				URL:        fmt.Sprintf("https://example.com/list/%d", i),
				Secret:     "secret",
				EventTypes: []string{"test.event"},
				Status:     SubscriptionStatusActive,
			}
			err = repo.CreateSubscription(ctx, sub)
			require.NoError(t, err)
		}

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		subs, err := tx.ListSubscriptions(ctx, &SubscriptionFilter{
			TenantID: "tenant_list_tx",
		})
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(subs), 3)
	})

	t.Run("list_includes_uncommitted", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		sub := &Subscription{
			ID:         "sub_tx_list_uncommitted",
			TenantID:   "tenant_uncommitted",
			URL:        "https://example.com/uncommitted",
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}

		err = tx.CreateSubscription(ctx, sub)
		require.NoError(t, err)

		// List within same transaction should include uncommitted
		subs, err := tx.ListSubscriptions(ctx, &SubscriptionFilter{
			TenantID: "tenant_uncommitted",
		})
		require.NoError(t, err)
		require.Len(t, subs, 1)
	})
}

// =============================================================================
// DELIVERY OPERATIONS IN TRANSACTION TESTS
// =============================================================================

func TestPostgresRepositoryTx_CreateDelivery_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription first
	sub := &Subscription{
		ID:         "sub_tx_delivery",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	t.Run("create_delivery_and_commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		delivery := &Delivery{
			ID:             "dlv_tx_1",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"data": "test"},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    10,
		}

		err = tx.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify persisted
		fetched, err := repo.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		require.Equal(t, delivery.ID, fetched.ID)
	})

	t.Run("create_delivery_and_rollback", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		delivery := &Delivery{
			ID:             "dlv_tx_rollback",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"data": "test"},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    10,
		}

		err = tx.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT persisted
		_, err = repo.GetDelivery(ctx, delivery.ID)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrDeliveryNotFound)
	})
}

func TestPostgresRepositoryTx_GetDelivery_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Setup
	sub := &Subscription{
		ID:         "sub_tx_get_delivery",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	delivery := &Delivery{
		ID:             "dlv_tx_get",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]interface{}{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	t.Run("get_existing_delivery", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		fetched, err := tx.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		require.Equal(t, delivery.ID, fetched.ID)
	})

	t.Run("get_non_existent", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		_, err = tx.GetDelivery(ctx, "dlv_non_existent")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrDeliveryNotFound)
	})
}

func TestPostgresRepositoryTx_UpdateDelivery_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Setup
	sub := &Subscription{
		ID:         "sub_tx_update_delivery",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	t.Run("update_and_commit", func(t *testing.T) {
		delivery := &Delivery{
			ID:             "dlv_tx_update_1",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    10,
		}
		err = repo.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		delivery.Status = DeliveryStatusSuccess
		delivery.AttemptCount = 1
		now := time.Now()
		delivery.CompletedAt = &now

		err = tx.UpdateDelivery(ctx, delivery)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify changes persisted
		updated, err := repo.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		require.Equal(t, DeliveryStatusSuccess, updated.Status)
		require.Equal(t, 1, updated.AttemptCount)
	})

	t.Run("update_and_rollback", func(t *testing.T) {
		delivery := &Delivery{
			ID:             "dlv_tx_update_rollback",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    10,
		}
		err = repo.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		delivery.Status = DeliveryStatusSuccess
		err = tx.UpdateDelivery(ctx, delivery)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify changes NOT persisted
		fetched, err := repo.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		require.Equal(t, DeliveryStatusPending, fetched.Status)
	})

	t.Run("update_non_existent", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		delivery := &Delivery{
			ID:             "dlv_does_not_exist",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusSuccess,
			AttemptCount:   1,
			MaxAttempts:    10,
		}

		err = tx.UpdateDelivery(ctx, delivery)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrDeliveryNotFound)
	})
}

func TestPostgresRepositoryTx_GetPendingDeliveries_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Setup
	sub := &Subscription{
		ID:         "sub_tx_pending",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	t.Run("get_pending_with_skip_locked", func(t *testing.T) {
		// Create pending deliveries
		for i := 0; i < 3; i++ {
			delivery := &Delivery{
				ID:             fmt.Sprintf("dlv_tx_pending_%d", i),
				SubscriptionID: sub.ID,
				TenantID:       sub.TenantID,
				EventType:      "test.event",
				Payload:        map[string]interface{}{"id": i},
				Status:         DeliveryStatusPending,
				AttemptCount:   0,
				MaxAttempts:    10,
			}
			err = repo.CreateDelivery(ctx, delivery)
			require.NoError(t, err)
		}

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		deliveries, err := tx.GetPendingDeliveries(ctx, 10)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(deliveries), 3)
	})

	t.Run("skip_locked_behavior", func(t *testing.T) {
		// Create a delivery
		delivery := &Delivery{
			ID:             "dlv_tx_skip_locked",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    10,
		}
		err = repo.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		// Lock it in tx1
		tx1, err := pgContainer.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx1.Rollback()

		_, err = tx1.ExecContext(ctx, `
			SELECT * FROM deliveries
			WHERE id = $1
			FOR UPDATE
		`, delivery.ID)
		require.NoError(t, err)

		// Try to get pending in tx2 - should skip locked
		tx2, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx2.Rollback()

		deliveries, err := tx2.GetPendingDeliveries(ctx, 10)
		require.NoError(t, err)

		// Verify locked delivery is skipped
		for _, d := range deliveries {
			require.NotEqual(t, delivery.ID, d.ID, "Locked delivery should be skipped")
		}
	})
}

func TestPostgresRepositoryTx_MoveToDeadLetter_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Setup
	sub := &Subscription{
		ID:         "sub_tx_dead_letter",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	t.Run("move_and_commit", func(t *testing.T) {
		delivery := &Delivery{
			ID:             "dlv_tx_dead_letter_1",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusPending,
			AttemptCount:   3,
			MaxAttempts:    3,
		}
		err = repo.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.MoveToDeadLetter(ctx, delivery.ID, "max retries exceeded")
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify status changed
		updated, err := repo.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		require.Equal(t, DeliveryStatusDeadLetter, updated.Status)
		require.NotNil(t, updated.CompletedAt)
	})

	t.Run("move_and_rollback", func(t *testing.T) {
		delivery := &Delivery{
			ID:             "dlv_tx_dead_letter_rollback",
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]interface{}{"test": "data"},
			Status:         DeliveryStatusPending,
			AttemptCount:   3,
			MaxAttempts:    3,
		}
		err = repo.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx.MoveToDeadLetter(ctx, delivery.ID, "max retries exceeded")
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify status NOT changed
		fetched, err := repo.GetDelivery(ctx, delivery.ID)
		require.NoError(t, err)
		require.Equal(t, DeliveryStatusPending, fetched.Status)
	})

	t.Run("move_non_existent", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		err = tx.MoveToDeadLetter(ctx, "dlv_does_not_exist", "reason")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrDeliveryNotFound)
	})
}

// =============================================================================
// DELIVERY ATTEMPT OPERATIONS IN TRANSACTION TESTS
// =============================================================================

func TestPostgresRepositoryTx_CreateDeliveryAttempt_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Setup
	sub := &Subscription{
		ID:         "sub_tx_attempt",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	delivery := &Delivery{
		ID:             "dlv_tx_attempt",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]interface{}{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	t.Run("create_and_commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		attempt := &DeliveryAttempt{
			ID:              "att_tx_1",
			DeliveryID:      delivery.ID,
			AttemptNumber:   1,
			StatusCode:      500,
			ResponseBody:    "Internal Server Error",
			ResponseHeaders: map[string]string{"Content-Type": "text/plain"},
			Error:           "connection timeout",
			DurationMs:      1500,
			AttemptedAt:     time.Now(),
		}

		err = tx.CreateDeliveryAttempt(ctx, attempt)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify persisted
		attempts, err := repo.GetDeliveryAttempts(ctx, delivery.ID)
		require.NoError(t, err)
		require.Len(t, attempts, 1)
		require.Equal(t, attempt.ID, attempts[0].ID)
	})

	t.Run("create_and_rollback", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		attempt := &DeliveryAttempt{
			ID:            "att_tx_rollback",
			DeliveryID:    delivery.ID,
			AttemptNumber: 2,
			StatusCode:    500,
			ResponseBody:  "Error",
			DurationMs:    1000,
			AttemptedAt:   time.Now(),
		}

		err = tx.CreateDeliveryAttempt(ctx, attempt)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT persisted
		attempts, err := repo.GetDeliveryAttempts(ctx, delivery.ID)
		require.NoError(t, err)
		// Should not include rolled back attempt
		for _, a := range attempts {
			require.NotEqual(t, "att_tx_rollback", a.ID)
		}
	})

	t.Run("duplicate_attempt_number", func(t *testing.T) {
		// Create first attempt
		attempt1 := &DeliveryAttempt{
			ID:            "att_tx_dup_1",
			DeliveryID:    delivery.ID,
			AttemptNumber: 5,
			StatusCode:    500,
			ResponseBody:  "Error",
			DurationMs:    1000,
			AttemptedAt:   time.Now(),
		}
		err = repo.CreateDeliveryAttempt(ctx, attempt1)
		require.NoError(t, err)

		// Try duplicate in transaction
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		attempt2 := &DeliveryAttempt{
			ID:            "att_tx_dup_2",
			DeliveryID:    delivery.ID,
			AttemptNumber: 5, // Same attempt number
			StatusCode:    500,
			ResponseBody:  "Error",
			DurationMs:    1000,
			AttemptedAt:   time.Now(),
		}

		err = tx.CreateDeliveryAttempt(ctx, attempt2)
		require.Error(t, err)
		// Should be a conflict error
		require.True(t, IsConflictError(err))
	})
}

func TestPostgresRepositoryTx_GetDeliveryAttempts_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Setup
	sub := &Subscription{
		ID:         "sub_tx_get_attempts",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	delivery := &Delivery{
		ID:             "dlv_tx_get_attempts",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]interface{}{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Create attempts
	for i := 1; i <= 3; i++ {
		attempt := &DeliveryAttempt{
			ID:            fmt.Sprintf("att_tx_get_%d", i),
			DeliveryID:    delivery.ID,
			AttemptNumber: i,
			StatusCode:    500 + i,
			ResponseBody:  fmt.Sprintf("Error %d", i),
			DurationMs:    int64(1000 * i),
			AttemptedAt:   time.Now().Add(time.Duration(i) * time.Second),
		}
		err = repo.CreateDeliveryAttempt(ctx, attempt)
		require.NoError(t, err)
	}

	t.Run("get_all_attempts", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		attempts, err := tx.GetDeliveryAttempts(ctx, delivery.ID)
		require.NoError(t, err)
		require.Len(t, attempts, 3)

		// Verify ordering
		for i, attempt := range attempts {
			require.Equal(t, i+1, attempt.AttemptNumber)
		}
	})

	t.Run("get_attempts_empty", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		attempts, err := tx.GetDeliveryAttempts(ctx, "dlv_no_attempts")
		require.NoError(t, err)
		require.Empty(t, attempts)
	})
}

// =============================================================================
// IDEMPOTENCY OPERATIONS IN TRANSACTION TESTS
// =============================================================================

func TestPostgresRepositoryTx_CheckIdempotency_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Setup
	sub := &Subscription{
		ID:         "sub_tx_idem",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	t.Run("check_existing_key", func(t *testing.T) {
		// Store key
		key := "idem_tx_test"
		expiresAt := time.Now().Add(24 * time.Hour)
		err = repo.StoreIdempotencyKey(ctx, key, sub.ID, expiresAt)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		exists, err := tx.CheckIdempotency(ctx, key, sub.ID)
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("check_non_existent_key", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		exists, err := tx.CheckIdempotency(ctx, "non_existent_key", sub.ID)
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("check_expired_key", func(t *testing.T) {
		// Store expired key
		key := "idem_tx_expired"
		expiresAt := time.Now().Add(-1 * time.Hour) // Expired
		err = repo.StoreIdempotencyKey(ctx, key, sub.ID, expiresAt)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		exists, err := tx.CheckIdempotency(ctx, key, sub.ID)
		require.NoError(t, err)
		require.False(t, exists, "Expired key should return false")
	})
}

func TestPostgresRepositoryTx_StoreIdempotencyKey_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Setup
	sub := &Subscription{
		ID:         "sub_tx_store_idem",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	t.Run("store_and_commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		key := "idem_tx_store_1"
		expiresAt := time.Now().Add(24 * time.Hour)
		err = tx.StoreIdempotencyKey(ctx, key, sub.ID, expiresAt)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify persisted
		exists, err := repo.CheckIdempotency(ctx, key, sub.ID)
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("store_and_rollback", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		key := "idem_tx_store_rollback"
		expiresAt := time.Now().Add(24 * time.Hour)
		err = tx.StoreIdempotencyKey(ctx, key, sub.ID, expiresAt)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT persisted
		exists, err := repo.CheckIdempotency(ctx, key, sub.ID)
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("upsert_behavior", func(t *testing.T) {
		key := "idem_tx_upsert"
		expiresAt1 := time.Now().Add(1 * time.Hour)

		// First insert
		tx1, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx1.StoreIdempotencyKey(ctx, key, sub.ID, expiresAt1)
		require.NoError(t, err)

		err = tx1.Commit()
		require.NoError(t, err)

		// Update with new expiration
		expiresAt2 := time.Now().Add(2 * time.Hour)
		tx2, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		err = tx2.StoreIdempotencyKey(ctx, key, sub.ID, expiresAt2)
		require.NoError(t, err)

		err = tx2.Commit()
		require.NoError(t, err)

		// Verify still exists (upsert worked)
		exists, err := repo.CheckIdempotency(ctx, key, sub.ID)
		require.NoError(t, err)
		require.True(t, exists)
	})
}

// =============================================================================
// CIRCUIT BREAKER OPERATIONS IN TRANSACTION TESTS
// =============================================================================

func TestPostgresRepositoryTx_GetCircuitBreakerState_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	endpoint := "https://example.com/webhook"

	t.Run("get_non_existent_returns_default", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		state, err := tx.GetCircuitBreakerState(ctx, "https://non-existent.com")
		require.NoError(t, err)
		require.NotNil(t, state)
		require.Equal(t, CircuitBreakerStateClosed, state.State)
		require.Equal(t, 0, state.FailureCount)
	})

	t.Run("get_existing_state", func(t *testing.T) {
		// Create state
		cbState := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateOpen,
			FailureCount: 5,
			SuccessCount: 0,
			LastFailure:  time.Now(),
			NextRetryAt:  time.Now().Add(1 * time.Minute),
		}
		err = repo.UpdateCircuitBreakerState(ctx, cbState)
		require.NoError(t, err)

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)
		defer tx.Rollback()

		state, err := tx.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		require.Equal(t, CircuitBreakerStateOpen, state.State)
		require.Equal(t, 5, state.FailureCount)
	})
}

func TestPostgresRepositoryTx_UpdateCircuitBreakerState_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	endpoint := "https://example.com/webhook"

	t.Run("update_and_commit", func(t *testing.T) {
		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		cbState := &CircuitBreakerState{
			Endpoint:     endpoint,
			State:        CircuitBreakerStateOpen,
			FailureCount: 5,
			SuccessCount: 0,
			LastFailure:  time.Now(),
			NextRetryAt:  time.Now().Add(1 * time.Minute),
		}

		err = tx.UpdateCircuitBreakerState(ctx, cbState)
		require.NoError(t, err)

		err = tx.Commit()
		require.NoError(t, err)

		// Verify persisted
		state, err := repo.GetCircuitBreakerState(ctx, endpoint)
		require.NoError(t, err)
		require.Equal(t, CircuitBreakerStateOpen, state.State)
		require.Equal(t, 5, state.FailureCount)
	})

	t.Run("update_and_rollback", func(t *testing.T) {
		endpoint2 := "https://example.com/webhook2"

		tx, err := repo.BeginTx(ctx)
		require.NoError(t, err)

		cbState := &CircuitBreakerState{
			Endpoint:     endpoint2,
			State:        CircuitBreakerStateOpen,
			FailureCount: 10,
			SuccessCount: 0,
		}

		err = tx.UpdateCircuitBreakerState(ctx, cbState)
		require.NoError(t, err)

		err = tx.Rollback()
		require.NoError(t, err)

		// Verify NOT persisted (should return default)
		state, err := repo.GetCircuitBreakerState(ctx, endpoint2)
		require.NoError(t, err)
		require.Equal(t, CircuitBreakerStateClosed, state.State)
		require.Equal(t, 0, state.FailureCount)
	})
}

// =============================================================================
// NESTED TRANSACTIONS AND SPECIAL CASES
// =============================================================================

func TestPostgresRepositoryTx_BeginTx_NotSupported_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	// Nested transactions not supported
	_, err = tx.BeginTx(ctx)
	require.Error(t, err)
	require.True(t, IsValidationError(err))
}

func TestPostgresRepositoryTx_Ping_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	// Ping should succeed (pings connection pool, not transaction)
	err = tx.Ping(ctx)
	require.NoError(t, err)
}

func TestPostgresRepositoryTx_Close_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	// Close is a no-op for transactions
	err = tx.Close()
	require.NoError(t, err)
}

// =============================================================================
// CONCURRENT TRANSACTION TESTS
// =============================================================================

func TestPostgresRepositoryTx_ConcurrentTransactions_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	t.Run("concurrent_commits", func(t *testing.T) {
		const numTxs = 10

		// Run concurrent transactions
		for i := 0; i < numTxs; i++ {
			go func(idx int) {
				tx, err := repo.BeginTx(ctx)
				require.NoError(t, err)

				sub := &Subscription{
					ID:         fmt.Sprintf("sub_concurrent_%d", idx),
					TenantID:   "tenant_concurrent",
					URL:        fmt.Sprintf("https://example.com/concurrent/%d", idx),
					Secret:     "secret",
					EventTypes: []string{"test.event"},
					Status:     SubscriptionStatusActive,
				}

				err = tx.CreateSubscription(ctx, sub)
				require.NoError(t, err)

				err = tx.Commit()
				require.NoError(t, err)
			}(i)
		}

		// Wait a bit for transactions to complete
		time.Sleep(1 * time.Second)

		// Verify all subscriptions created
		subs, err := repo.ListSubscriptions(ctx, &SubscriptionFilter{
			TenantID: "tenant_concurrent",
		})
		require.NoError(t, err)
		require.Len(t, subs, numTxs)
	})
}

// =============================================================================
// ERROR PATH TESTS
// =============================================================================

func TestPostgresRepositoryTx_ErrorAfterCommit_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	sub := &Subscription{
		ID:         "sub_tx_after_commit",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/after-commit",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}

	err = tx.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// Trying to use transaction after commit should fail
	// Note: PostgreSQL returns sql.ErrTxDone which our code handles gracefully
	sub2 := &Subscription{
		ID:         "sub_tx_after_commit_2",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/after-commit-2",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}

	err = tx.CreateSubscription(ctx, sub2)
	require.Error(t, err)
}

func TestPostgresRepositoryTx_ContextCancellation_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr)
	require.NoError(t, err)
	defer repo.Close()

	ctx, cancel := context.WithCancel(context.Background())

	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	sub := &Subscription{
		ID:         "sub_tx_cancel",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/cancel",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}

	// Cancel context
	cancel()

	// Operations with cancelled context should fail
	err = tx.CreateSubscription(ctx, sub)
	require.Error(t, err)
}
