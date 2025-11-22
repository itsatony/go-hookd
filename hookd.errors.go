// Package internal provides the core webhook management implementation for go-hookd.
//
// This file defines all error types using go-cuserr for consistent, protocol-agnostic
// error handling throughout the package.
//
// All errors use go-cuserr's sentinel errors and constructor functions to ensure
// proper categorization and automatic mapping to HTTP status codes, gRPC codes, etc.
package hookd

import (
	"fmt"

	"github.com/itsatony/go-cuserr"
)

// Sentinel Errors - Reusable error instances for common cases
//
// These can be used for error type checking with standard error comparison.
var (
	// ErrSubscriptionNotFound indicates a subscription was not found.
	ErrSubscriptionNotFound = cuserr.NewNotFoundError("subscription", "")

	// ErrDeliveryNotFound indicates a delivery was not found.
	ErrDeliveryNotFound = cuserr.NewNotFoundError("delivery", "")

	// ErrCircuitBreakerNotFound indicates circuit breaker state was not found.
	ErrCircuitBreakerNotFound = cuserr.NewNotFoundError("circuit_breaker", "")

	// ErrCircuitBreakerOpen indicates the circuit breaker is open (fast-fail).
	ErrCircuitBreakerOpen = cuserr.NewExternalError("circuit_breaker", "webhook", nil)

	// ErrSubscriptionNotActive indicates the subscription is not in active status.
	ErrSubscriptionNotActive = cuserr.NewValidationError("subscription", ErrMsgSubscriptionNotActive)

	// ErrDeliveryAlreadyCompleted indicates the delivery has already completed.
	ErrDeliveryAlreadyCompleted = cuserr.NewConflictError("delivery", "status", ErrMsgDeliveryAlreadyCompleted)

	// ErrDuplicateSubscription indicates a subscription already exists for tenant+URL.
	ErrDuplicateSubscription = cuserr.NewConflictError("subscription", "url", ErrMsgSubscriptionExists)
)

// Error Constructor Functions - Create contextual errors with metadata

// NewSubscriptionNotFoundError creates a not found error for a subscription.
func NewSubscriptionNotFoundError(subscriptionID string) error {
	return cuserr.NewNotFoundError("subscription", subscriptionID)
}

// NewDeliveryNotFoundError creates a not found error for a delivery.
func NewDeliveryNotFoundError(deliveryID string) error {
	return cuserr.NewNotFoundError("delivery", deliveryID)
}

// NewValidationError creates a validation error with field context.
func NewValidationError(field, message string) error {
	return cuserr.NewValidationError(field, message)
}

// NewDatabaseError creates an internal error for database operations.
//
// Use this wrapper to add context about the database operation that failed.
func NewDatabaseError(operation string, err error) error {
	return cuserr.NewInternalError("database", err,
		cuserr.WithMetadata("operation", operation),
	)
}

// NewDeliveryExecutionError creates an external error for webhook delivery failures.
//
// This categorizes the error as an external service failure, which is critical
// for retry logic and circuit breaker decisions.
func NewDeliveryExecutionError(url string, statusCode int, err error) error {
	return cuserr.NewExternalError("webhook-endpoint", "POST", err,
		cuserr.WithMetadata("url", url),
		cuserr.WithMetadata("status_code", fmt.Sprintf("%d", statusCode)),
	)
}

// NewCircuitBreakerError creates an error indicating circuit breaker is open.
func NewCircuitBreakerError(endpoint string) error {
	// Circuit breaker open is an external service error (service unavailable)
	return cuserr.NewExternalError("webhook-endpoint", "circuit_breaker",
		fmt.Errorf(ErrMsgCircuitBreakerOpen),
		cuserr.WithMetadata("endpoint", endpoint),
	)
}

// NewIdempotencyError creates a conflict error for duplicate idempotency keys.
func NewIdempotencyError(key string) error {
	return cuserr.NewConflictError("delivery", "idempotency_key", fmt.Sprintf("%s: %s", ErrMsgDuplicateDelivery, key))
}

// NewConfigurationError creates a validation error for configuration issues.
func NewConfigurationError(field, message string) error {
	return cuserr.NewValidationError(field, message)
}

// NewSubscriptionExistsError creates a conflict error for duplicate subscriptions.
func NewSubscriptionExistsError(tenantID, url string) error {
	return cuserr.NewConflictError("subscription", "url", fmt.Sprintf("%s (tenant: %s, url: %s)", ErrMsgSubscriptionExists, tenantID, url))
}

// NewDeliveryTimeoutError creates a timeout error for delivery attempts.
func NewDeliveryTimeoutError(url string, duration string, err error) error {
	// Create timeout error and add metadata
	return cuserr.NewTimeoutError("webhook-delivery", err).
		WithMetadata("url", url).
		WithMetadata("duration", duration)
}

// NewRateLimitError creates a rate limit error.
func NewRateLimitError(limit int, window string) error {
	return cuserr.NewRateLimitError(fmt.Sprintf("%d", limit), window)
}

// IsNotFoundError checks if an error is a not found error.
func IsNotFoundError(err error) bool {
	return cuserr.IsErrorCategory(err, cuserr.ErrorCategoryNotFound)
}

// IsValidationError checks if an error is a validation error.
func IsValidationError(err error) bool {
	return cuserr.IsErrorCategory(err, cuserr.ErrorCategoryValidation)
}

// IsConflictError checks if an error is a conflict error.
func IsConflictError(err error) bool {
	return cuserr.IsErrorCategory(err, cuserr.ErrorCategoryConflict)
}

// IsExternalError checks if an error is an external service error.
//
// External errors are retriable and used for circuit breaker decisions.
func IsExternalError(err error) bool {
	return cuserr.IsErrorCategory(err, cuserr.ErrorCategoryExternal)
}

// IsInternalError checks if an error is an internal error.
func IsInternalError(err error) bool {
	return cuserr.IsErrorCategory(err, cuserr.ErrorCategoryInternal)
}

// IsTimeoutError checks if an error is a timeout error.
func IsTimeoutError(err error) bool {
	return cuserr.IsErrorCategory(err, cuserr.ErrorCategoryTimeout)
}

// IsRateLimitError checks if an error is a rate limit error.
func IsRateLimitError(err error) bool {
	return cuserr.IsErrorCategory(err, cuserr.ErrorCategoryRateLimit)
}

// ShouldRetry determines if an error should trigger a retry.
//
// Retry logic:
// - External errors: YES (network issues, 5xx status codes)
// - Timeout errors: YES
// - Rate limit errors: YES
// - Validation errors: NO (4xx status codes)
// - Not found errors: NO
// - Conflict errors: NO
// - Internal errors: NO (our bug, not endpoint's fault).
func ShouldRetry(err error) bool {
	if err == nil {
		return false
	}

	// Retry external service errors (network, 5xx)
	if IsExternalError(err) {
		return true
	}

	// Retry timeout errors
	if IsTimeoutError(err) {
		return true
	}

	// Retry rate limit errors (after backoff)
	if IsRateLimitError(err) {
		return true
	}

	// Don't retry validation errors (client error)
	if IsValidationError(err) {
		return false
	}

	// Don't retry not found errors
	if IsNotFoundError(err) {
		return false
	}

	// Don't retry conflict errors
	if IsConflictError(err) {
		return false
	}

	// Don't retry internal errors (our problem, not endpoint's)
	if IsInternalError(err) {
		return false
	}

	// Unknown error type - don't retry to be safe
	return false
}
