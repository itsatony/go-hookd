package internal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// COVERAGE IMPROVEMENT TESTS
// These tests target functions with 0% or low coverage to reach 90%+ target
// Only includes tests NOT already covered in other test files
// =============================================================================

// =============================================================================
// NO-OP EVENT BUS TESTS
// =============================================================================

func TestNoOpEventBus_Publish(t *testing.T) {
	t.Run("publish does not panic", func(t *testing.T) {
		bus := &noOpEventBus{}

		// Should not panic - no-op implementation
		assert.NotPanics(t, func() {
			bus.Publish("test.topic", map[string]string{"key": "value"})
		})

		// Test with nil data
		assert.NotPanics(t, func() {
			bus.Publish("test.topic", nil)
		})
	})
}

func TestNoOpEventBus_Subscribe(t *testing.T) {
	t.Run("subscribe returns no-op unsubscribe function", func(t *testing.T) {
		bus := &noOpEventBus{}

		handlerCalled := false
		handler := func(data interface{}) {
			handlerCalled = true
		}

		// Subscribe returns a function
		unsubscribe := bus.Subscribe("test.topic", handler)
		assert.NotNil(t, unsubscribe)

		// Unsubscribe should not panic
		assert.NotPanics(t, func() {
			unsubscribe()
		})

		// Handler should never be called (no-op implementation)
		assert.False(t, handlerCalled)
	})
}

// =============================================================================
// MOCK REPOSITORY ADDITIONAL COVERAGE TESTS
// Only tests for functions not already covered
// =============================================================================

func TestMockRepository_UnlockDelivery(t *testing.T) {
	t.Run("unlock delivery success", func(t *testing.T) {
		repo := NewMockRepository()
		ctx := context.Background()

		// Create a delivery
		delivery := &Delivery{
			ID:             "dlv_test123",
			SubscriptionID: "sub_test123",
			TenantID:       "tenant_1",
			EventType:      "test.event",
			Payload:        map[string]interface{}{"key": "value"},
			Status:         DeliveryStatusPending,
			MaxAttempts:    3,
		}

		err := repo.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		// Unlock should not panic (no-op for non-locked deliveries)
		assert.NotPanics(t, func() {
			repo.UnlockDelivery(delivery.ID)
		})
	})

	t.Run("unlock non-existent delivery does not panic", func(t *testing.T) {
		repo := NewMockRepository()

		// Should not panic for non-existent delivery
		assert.NotPanics(t, func() {
			repo.UnlockDelivery("dlv_nonexistent")
		})
	})
}

func TestMockRepository_GetDelivery(t *testing.T) {
	t.Run("get delivery success", func(t *testing.T) {
		repo := NewMockRepository()
		ctx := context.Background()

		delivery := &Delivery{
			ID:             "dlv_test123",
			SubscriptionID: "sub_test123",
			TenantID:       "tenant_1",
			EventType:      "test.event",
			Payload:        map[string]interface{}{"key": "value"},
			Status:         DeliveryStatusPending,
			MaxAttempts:    3,
		}

		err := repo.CreateDelivery(ctx, delivery)
		require.NoError(t, err)

		found, err := repo.GetDelivery(ctx, delivery.ID)
		assert.NoError(t, err)
		assert.Equal(t, delivery.ID, found.ID)
	})
}

func TestMockRepository_Transaction(t *testing.T) {
	t.Run("begin transaction", func(t *testing.T) {
		repo := NewMockRepository()
		ctx := context.Background()

		tx, err := repo.BeginTx(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, tx)
	})
}
