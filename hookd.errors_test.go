package hookd

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestErrorConstructors tests error constructor functions.
func TestErrorConstructors(t *testing.T) {
	t.Run("NewSubscriptionNotFoundError", func(t *testing.T) {
		err := NewSubscriptionNotFoundError("sub_123")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sub_123")
		assert.True(t, IsNotFoundError(err))
	})

	t.Run("NewDeliveryNotFoundError", func(t *testing.T) {
		err := NewDeliveryNotFoundError("dlv_456")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "dlv_456")
		assert.True(t, IsNotFoundError(err))
	})

	t.Run("NewValidationError", func(t *testing.T) {
		err := NewValidationError("email", "invalid format")
		assert.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("NewDatabaseError", func(t *testing.T) {
		originalErr := fmt.Errorf("connection failed")
		err := NewDatabaseError("SELECT", originalErr)
		assert.Error(t, err)
		assert.True(t, IsInternalError(err))
	})

	t.Run("NewDeliveryExecutionError", func(t *testing.T) {
		originalErr := fmt.Errorf("timeout")
		err := NewDeliveryExecutionError("https://example.com", 503, originalErr)
		assert.Error(t, err)
		assert.True(t, IsExternalError(err))
	})

	t.Run("NewCircuitBreakerError", func(t *testing.T) {
		err := NewCircuitBreakerError("https://example.com")
		assert.Error(t, err)
		assert.True(t, IsExternalError(err))
		assert.Contains(t, err.Error(), ErrMsgCircuitBreakerOpen)
	})

	t.Run("NewIdempotencyError", func(t *testing.T) {
		err := NewIdempotencyError("key_123")
		assert.Error(t, err)
		assert.True(t, IsConflictError(err))
		assert.Contains(t, err.Error(), "key_123")
	})

	t.Run("NewConfigurationError", func(t *testing.T) {
		err := NewConfigurationError("worker_count", "must be positive")
		assert.Error(t, err)
		assert.True(t, IsValidationError(err))
	})

	t.Run("NewSubscriptionExistsError", func(t *testing.T) {
		err := NewSubscriptionExistsError("tenant_1", "https://example.com")
		assert.Error(t, err)
		assert.True(t, IsConflictError(err))
		assert.Contains(t, err.Error(), "tenant_1")
		assert.Contains(t, err.Error(), "https://example.com")
	})

	t.Run("NewDeliveryTimeoutError", func(t *testing.T) {
		originalErr := fmt.Errorf("context deadline exceeded")
		err := NewDeliveryTimeoutError("https://example.com", "30s", originalErr)
		assert.Error(t, err)
		assert.True(t, IsTimeoutError(err))
		// go-cuserr metadata is not included in error string by default
		assert.Contains(t, err.Error(), "webhook-delivery")
		assert.Contains(t, err.Error(), "timed out")
	})

	t.Run("NewRateLimitError", func(t *testing.T) {
		err := NewRateLimitError(100, "1m")
		assert.Error(t, err)
		assert.True(t, IsRateLimitError(err))
	})
}

// TestErrorCategoryCheckers tests the Is*Error functions.
func TestErrorCategoryCheckers(t *testing.T) {
	t.Run("IsNotFoundError", func(t *testing.T) {
		err := NewSubscriptionNotFoundError("sub_123")
		assert.True(t, IsNotFoundError(err))
		assert.False(t, IsValidationError(err))
		assert.False(t, IsConflictError(err))
	})

	t.Run("IsValidationError", func(t *testing.T) {
		err := NewValidationError("field", "message")
		assert.True(t, IsValidationError(err))
		assert.False(t, IsNotFoundError(err))
		assert.False(t, IsConflictError(err))
	})

	t.Run("IsConflictError", func(t *testing.T) {
		err := NewIdempotencyError("key")
		assert.True(t, IsConflictError(err))
		assert.False(t, IsNotFoundError(err))
		assert.False(t, IsValidationError(err))
	})

	t.Run("IsExternalError", func(t *testing.T) {
		err := NewDeliveryExecutionError("url", 500, nil)
		assert.True(t, IsExternalError(err))
		assert.False(t, IsInternalError(err))
	})

	t.Run("IsInternalError", func(t *testing.T) {
		err := NewDatabaseError("query", fmt.Errorf("sql error"))
		assert.True(t, IsInternalError(err))
		assert.False(t, IsExternalError(err))
	})

	t.Run("IsTimeoutError", func(t *testing.T) {
		err := NewDeliveryTimeoutError("url", "30s", nil)
		assert.True(t, IsTimeoutError(err))
		assert.False(t, IsExternalError(err))
	})

	t.Run("IsRateLimitError", func(t *testing.T) {
		err := NewRateLimitError(100, "1m")
		assert.True(t, IsRateLimitError(err))
		assert.False(t, IsExternalError(err))
	})

	t.Run("nil error returns false", func(t *testing.T) {
		assert.False(t, IsNotFoundError(nil))
		assert.False(t, IsValidationError(nil))
		assert.False(t, IsConflictError(nil))
		assert.False(t, IsExternalError(nil))
		assert.False(t, IsInternalError(nil))
		assert.False(t, IsTimeoutError(nil))
		assert.False(t, IsRateLimitError(nil))
	})

	t.Run("non-cuserr error returns false", func(t *testing.T) {
		err := fmt.Errorf("plain error")
		assert.False(t, IsNotFoundError(err))
		assert.False(t, IsValidationError(err))
		assert.False(t, IsConflictError(err))
		assert.False(t, IsExternalError(err))
		assert.False(t, IsInternalError(err))
	})
}

// TestShouldRetry tests the retry logic function.
func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		shouldRetry bool
	}{
		{
			name:        "nil error - no retry",
			err:         nil,
			shouldRetry: false,
		},
		{
			name:        "external error - retry",
			err:         NewDeliveryExecutionError("url", 500, nil),
			shouldRetry: true,
		},
		{
			name:        "timeout error - retry",
			err:         NewDeliveryTimeoutError("url", "30s", nil),
			shouldRetry: true,
		},
		{
			name:        "rate limit error - retry",
			err:         NewRateLimitError(100, "1m"),
			shouldRetry: true,
		},
		{
			name:        "validation error - no retry",
			err:         NewValidationError("field", "message"),
			shouldRetry: false,
		},
		{
			name:        "not found error - no retry",
			err:         NewSubscriptionNotFoundError("sub_123"),
			shouldRetry: false,
		},
		{
			name:        "conflict error - no retry",
			err:         NewIdempotencyError("key"),
			shouldRetry: false,
		},
		{
			name:        "internal error - no retry",
			err:         NewDatabaseError("query", fmt.Errorf("error")),
			shouldRetry: false,
		},
		{
			name:        "circuit breaker error - retry (it's external)",
			err:         NewCircuitBreakerError("endpoint"),
			shouldRetry: true,
		},
		{
			name:        "plain error - no retry",
			err:         fmt.Errorf("unknown error"),
			shouldRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.shouldRetry, ShouldRetry(tt.err))
		})
	}
}

// TestSentinelErrors tests sentinel error constants.
func TestSentinelErrors(t *testing.T) {
	t.Run("ErrSubscriptionNotFound", func(t *testing.T) {
		assert.Error(t, ErrSubscriptionNotFound)
		assert.True(t, IsNotFoundError(ErrSubscriptionNotFound))
	})

	t.Run("ErrDeliveryNotFound", func(t *testing.T) {
		assert.Error(t, ErrDeliveryNotFound)
		assert.True(t, IsNotFoundError(ErrDeliveryNotFound))
	})

	t.Run("ErrCircuitBreakerNotFound", func(t *testing.T) {
		assert.Error(t, ErrCircuitBreakerNotFound)
		assert.True(t, IsNotFoundError(ErrCircuitBreakerNotFound))
	})

	t.Run("ErrCircuitBreakerOpen", func(t *testing.T) {
		assert.Error(t, ErrCircuitBreakerOpen)
		assert.True(t, IsExternalError(ErrCircuitBreakerOpen))
	})

	t.Run("ErrSubscriptionNotActive", func(t *testing.T) {
		assert.Error(t, ErrSubscriptionNotActive)
		assert.True(t, IsValidationError(ErrSubscriptionNotActive))
	})

	t.Run("ErrDeliveryAlreadyCompleted", func(t *testing.T) {
		assert.Error(t, ErrDeliveryAlreadyCompleted)
		assert.True(t, IsConflictError(ErrDeliveryAlreadyCompleted))
	})

	t.Run("ErrDuplicateSubscription", func(t *testing.T) {
		assert.Error(t, ErrDuplicateSubscription)
		assert.True(t, IsConflictError(ErrDuplicateSubscription))
	})
}
