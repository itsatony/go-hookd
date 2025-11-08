package internal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// CREATE SUBSCRIPTION TESTS
// =============================================================================

func TestCreateSubscription(t *testing.T) {
	t.Run("success with valid request", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		req := &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created", "user.updated"},
			Secret:     "test_secret",
		}

		sub, err := manager.CreateSubscription(ctx, req)

		require.NoError(t, err)
		assert.NotNil(t, sub)
		assert.NotEmpty(t, sub.ID)
		assert.Equal(t, req.TenantID, sub.TenantID)
		assert.Equal(t, req.URL, sub.URL)
		assert.Equal(t, req.EventTypes, sub.EventTypes)
		assert.Equal(t, req.Secret, sub.Secret)
		assert.Equal(t, SubscriptionStatusActive, sub.Status)
		assert.NotNil(t, sub.RetryPolicy)
		assert.NotZero(t, sub.CreatedAt)
		assert.NotZero(t, sub.UpdatedAt)
	})

	t.Run("success with custom retry policy", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		customPolicy := &RetryPolicy{
			MaxAttempts:    5,
			InitialBackoff: 2 * time.Second,
			MaxBackoff:     60 * time.Second,
			BackoffFactor:  2.5,
		}

		req := &CreateSubscriptionRequest{
			TenantID:    "tenant_123",
			URL:         "https://example.com/webhook",
			EventTypes:  []string{"user.created"},
			Secret:      "test_secret",
			RetryPolicy: customPolicy,
		}

		sub, err := manager.CreateSubscription(ctx, req)

		require.NoError(t, err)
		assert.Equal(t, customPolicy, sub.RetryPolicy)
	})

	t.Run("success with custom headers and metadata", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		headers := map[string]string{"X-Custom": "value"}
		metadata := map[string]interface{}{"key": "value"}

		req := &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
			Headers:    headers,
			Metadata:   metadata,
		}

		sub, err := manager.CreateSubscription(ctx, req)

		require.NoError(t, err)
		assert.Equal(t, headers, sub.Headers)
		assert.Equal(t, metadata, sub.Metadata)
	})

	t.Run("error with invalid request", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		req := &CreateSubscriptionRequest{
			TenantID: "", // Missing required field
			URL:      "https://example.com/webhook",
		}

		sub, err := manager.CreateSubscription(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, sub)
	})

	t.Run("error with duplicate subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		req := &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		}

		// Create first subscription
		_, err := manager.CreateSubscription(ctx, req)
		require.NoError(t, err)

		// Try to create duplicate
		sub2, err2 := manager.CreateSubscription(ctx, req)

		assert.Error(t, err2)
		assert.Nil(t, sub2)
		assert.Contains(t, err2.Error(), "already exists")
	})

	t.Run("error with invalid retry policy", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		invalidPolicy := &RetryPolicy{
			MaxAttempts:    -1, // Invalid
			InitialBackoff: time.Second,
		}

		req := &CreateSubscriptionRequest{
			TenantID:    "tenant_123",
			URL:         "https://example.com/webhook",
			EventTypes:  []string{"user.created"},
			Secret:      "test_secret",
			RetryPolicy: invalidPolicy,
		}

		sub, err := manager.CreateSubscription(ctx, req)

		assert.Error(t, err)
		assert.Nil(t, sub)
	})
}

// =============================================================================
// GET SUBSCRIPTION TESTS
// =============================================================================

func TestGetSubscription(t *testing.T) {
	t.Run("success with existing subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		createReq := &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		}
		created, _ := manager.CreateSubscription(ctx, createReq)

		// Get subscription
		sub, err := manager.GetSubscription(ctx, created.ID)

		require.NoError(t, err)
		assert.NotNil(t, sub)
		assert.Equal(t, created.ID, sub.ID)
		assert.Equal(t, created.TenantID, sub.TenantID)
		assert.Equal(t, created.URL, sub.URL)
	})

	t.Run("error with empty ID", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub, err := manager.GetSubscription(ctx, "")

		assert.Error(t, err)
		assert.Nil(t, sub)

	})

	t.Run("error with non-existent subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub, err := manager.GetSubscription(ctx, "sub_nonexistent")

		assert.Error(t, err)
		assert.Nil(t, sub)
		assert.Contains(t, err.Error(), "not found")
	})
}

// =============================================================================
// UPDATE SUBSCRIPTION TESTS
// =============================================================================

func TestUpdateSubscription(t *testing.T) {
	t.Run("success updating URL", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Update URL
		newURL := "https://new-url.com/webhook"
		updateReq := &UpdateSubscriptionRequest{
			URL: &newURL,
		}

		updated, err := manager.UpdateSubscription(ctx, created.ID, updateReq)

		require.NoError(t, err)
		assert.Equal(t, newURL, updated.URL)
		assert.True(t, updated.UpdatedAt.After(created.UpdatedAt))
	})

	t.Run("success updating event types", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Update event types
		newEventTypes := []string{"user.created", "user.updated", "user.deleted"}
		updateReq := &UpdateSubscriptionRequest{
			EventTypes: &newEventTypes,
		}

		updated, err := manager.UpdateSubscription(ctx, created.ID, updateReq)

		require.NoError(t, err)
		assert.Equal(t, newEventTypes, updated.EventTypes)
	})

	t.Run("success updating retry policy", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Update retry policy
		newPolicy := &RetryPolicy{
			MaxAttempts:    10,
			InitialBackoff: 5 * time.Second,
			MaxBackoff:     120 * time.Second,
			BackoffFactor:  3.0,
		}
		updateReq := &UpdateSubscriptionRequest{
			RetryPolicy: newPolicy,
		}

		updated, err := manager.UpdateSubscription(ctx, created.ID, updateReq)

		require.NoError(t, err)
		assert.Equal(t, newPolicy.MaxAttempts, updated.RetryPolicy.MaxAttempts)
	})

	t.Run("success updating headers and metadata", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Update headers and metadata
		newHeaders := map[string]string{"X-Custom": "new-value"}
		newMetadata := map[string]interface{}{"updated": true}
		updateReq := &UpdateSubscriptionRequest{
			Headers:  &newHeaders,
			Metadata: &newMetadata,
		}

		updated, err := manager.UpdateSubscription(ctx, created.ID, updateReq)

		require.NoError(t, err)
		assert.Equal(t, newHeaders, updated.Headers)
		assert.Equal(t, newMetadata, updated.Metadata)
	})

	t.Run("success updating status", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Update status
		newStatus := SubscriptionStatusPaused
		updateReq := &UpdateSubscriptionRequest{
			Status: &newStatus,
		}

		updated, err := manager.UpdateSubscription(ctx, created.ID, updateReq)

		require.NoError(t, err)
		assert.Equal(t, SubscriptionStatusPaused, updated.Status)
	})

	t.Run("no changes returns existing subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Update with no changes
		updateReq := &UpdateSubscriptionRequest{}

		updated, err := manager.UpdateSubscription(ctx, created.ID, updateReq)

		require.NoError(t, err)
		assert.Equal(t, created.UpdatedAt, updated.UpdatedAt)
	})

	t.Run("error with empty ID", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		updateReq := &UpdateSubscriptionRequest{}

		sub, err := manager.UpdateSubscription(ctx, "", updateReq)

		assert.Error(t, err)
		assert.Nil(t, sub)

	})

	t.Run("error with non-existent subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		updateReq := &UpdateSubscriptionRequest{}

		sub, err := manager.UpdateSubscription(ctx, "sub_nonexistent", updateReq)

		assert.Error(t, err)
		assert.Nil(t, sub)
	})

	t.Run("error with invalid status", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Update with invalid status
		invalidStatus := "invalid_status"
		updateReq := &UpdateSubscriptionRequest{
			Status: &invalidStatus,
		}

		sub, err := manager.UpdateSubscription(ctx, created.ID, updateReq)

		assert.Error(t, err)
		assert.Nil(t, sub)

	})

	t.Run("error with invalid retry policy", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Update with invalid retry policy
		invalidPolicy := &RetryPolicy{
			MaxAttempts: -1,
		}
		updateReq := &UpdateSubscriptionRequest{
			RetryPolicy: invalidPolicy,
		}

		sub, err := manager.UpdateSubscription(ctx, created.ID, updateReq)

		assert.Error(t, err)
		assert.Nil(t, sub)
	})
}

// =============================================================================
// DELETE SUBSCRIPTION TESTS
// =============================================================================

func TestDeleteSubscription(t *testing.T) {
	t.Run("success deleting existing subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Delete subscription
		err := manager.DeleteSubscription(ctx, created.ID)

		require.NoError(t, err)

		// Verify it's deleted
		sub, err := manager.GetSubscription(ctx, created.ID)
		assert.Error(t, err)
		assert.Nil(t, sub)
	})

	t.Run("error with empty ID", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		err := manager.DeleteSubscription(ctx, "")

		assert.Error(t, err)

	})

	t.Run("error with non-existent subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		err := manager.DeleteSubscription(ctx, "sub_nonexistent")

		assert.Error(t, err)
	})
}

// =============================================================================
// LIST SUBSCRIPTIONS TESTS
// =============================================================================

func TestListSubscriptions(t *testing.T) {
	t.Run("success listing all subscriptions for tenant", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create multiple subscriptions with unique URLs
		for i := 0; i < 3; i++ {
			manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
				TenantID:   "tenant_123",
				URL:        fmt.Sprintf("https://example.com/webhook%d", i),
				EventTypes: []string{"user.created"},
				Secret:     "test_secret",
			})
		}

		// List subscriptions
		filter := &SubscriptionFilter{
			TenantID: "tenant_123",
		}

		subs, err := manager.ListSubscriptions(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, subs, 3)
	})

	t.Run("success filtering by status", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create active subscription
		manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook1",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Create and pause subscription
		sub2, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook2",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})
		manager.PauseSubscription(ctx, sub2.ID)

		// List active subscriptions
		filter := &SubscriptionFilter{
			TenantID: "tenant_123",
			Status:   SubscriptionStatusActive,
		}

		subs, err := manager.ListSubscriptions(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, subs, 1)
		assert.Equal(t, SubscriptionStatusActive, subs[0].Status)
	})

	t.Run("success filtering by event type", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription with user.created
		manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook1",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Create subscription with order.created
		manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook2",
			EventTypes: []string{"order.created"},
			Secret:     "test_secret",
		})

		// List subscriptions with user.created
		filter := &SubscriptionFilter{
			TenantID:   "tenant_123",
			EventTypes: []string{"user.created"},
		}

		subs, err := manager.ListSubscriptions(ctx, filter)

		require.NoError(t, err)
		assert.Len(t, subs, 1)
		assert.Contains(t, subs[0].EventTypes, "user.created")
	})

	t.Run("error with nil filter", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		subs, err := manager.ListSubscriptions(ctx, nil)

		assert.Error(t, err)
		assert.Nil(t, subs)

	})

	t.Run("error with missing tenant ID", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()
		filter := &SubscriptionFilter{
			TenantID: "",
		}

		subs, err := manager.ListSubscriptions(ctx, filter)

		assert.Error(t, err)
		assert.Nil(t, subs)

	})
}

// =============================================================================
// CONVENIENCE METHOD TESTS
// =============================================================================

func TestPauseSubscription(t *testing.T) {
	t.Run("success pausing active subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Pause subscription
		paused, err := manager.PauseSubscription(ctx, created.ID)

		require.NoError(t, err)
		assert.Equal(t, SubscriptionStatusPaused, paused.Status)
	})

	t.Run("error with non-existent subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub, err := manager.PauseSubscription(ctx, "sub_nonexistent")

		assert.Error(t, err)
		assert.Nil(t, sub)
	})
}

func TestResumeSubscription(t *testing.T) {
	t.Run("success resuming paused subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create and pause subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})
		manager.PauseSubscription(ctx, created.ID)

		// Resume subscription
		resumed, err := manager.ResumeSubscription(ctx, created.ID)

		require.NoError(t, err)
		assert.Equal(t, SubscriptionStatusActive, resumed.Status)
	})

	t.Run("error with non-existent subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub, err := manager.ResumeSubscription(ctx, "sub_nonexistent")

		assert.Error(t, err)
		assert.Nil(t, sub)
	})
}

func TestDisableSubscription(t *testing.T) {
	t.Run("success disabling active subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		// Create subscription
		created, _ := manager.CreateSubscription(ctx, &CreateSubscriptionRequest{
			TenantID:   "tenant_123",
			URL:        "https://example.com/webhook",
			EventTypes: []string{"user.created"},
			Secret:     "test_secret",
		})

		// Disable subscription
		disabled, err := manager.DisableSubscription(ctx, created.ID)

		require.NoError(t, err)
		assert.Equal(t, SubscriptionStatusDisabled, disabled.Status)
	})

	t.Run("error with non-existent subscription", func(t *testing.T) {
		config := NewConfig("postgres://localhost/test")
		repo := NewMockRepository()
		manager, _ := NewManager(config, repo)

		ctx := context.Background()

		sub, err := manager.DisableSubscription(ctx, "sub_nonexistent")

		assert.Error(t, err)
		assert.Nil(t, sub)
	})
}
