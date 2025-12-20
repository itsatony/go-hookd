// Package internal provides the core webhook management implementation for go-hookd.
//
// This file defines the core interfaces for the package. All implementations must
// satisfy these interfaces to ensure proper abstraction and testability.
package hookd

import (
	"context"
	"time"
)

// Repository defines the data persistence interface for webhook management.
//
// All storage operations are abstracted through this interface, allowing for
// multiple implementations (PostgreSQL, mock, etc.) and simplified testing.
//
// Thread Safety: All Repository implementations must be safe for concurrent use.
type Repository interface {
	// Subscription Operations

	// CreateSubscription creates a new subscription in the data store.
	// Returns an error if the subscription already exists or if creation fails.
	CreateSubscription(ctx context.Context, sub *Subscription) error

	// GetSubscription retrieves a subscription by its ID.
	// Returns ErrSubscriptionNotFound if the subscription does not exist.
	GetSubscription(ctx context.Context, id string) (*Subscription, error)

	// GetSubscriptionByTenantAndURL retrieves a subscription by tenant ID and URL.
	// This is used to check for duplicates during creation.
	// Returns ErrSubscriptionNotFound if no matching subscription exists.
	GetSubscriptionByTenantAndURL(ctx context.Context, tenantID, url string) (*Subscription, error)

	// UpdateSubscription updates an existing subscription.
	// Returns ErrSubscriptionNotFound if the subscription does not exist.
	UpdateSubscription(ctx context.Context, sub *Subscription) error

	// DeleteSubscription deletes a subscription by its ID.
	// Returns ErrSubscriptionNotFound if the subscription does not exist.
	// Note: This cascades to delete all related deliveries and attempts.
	DeleteSubscription(ctx context.Context, id string) error

	// ListSubscriptions retrieves subscriptions matching the given filter.
	// Returns an empty slice if no subscriptions match.
	ListSubscriptions(ctx context.Context, filter *SubscriptionFilter) ([]*Subscription, error)

	// Delivery Operations

	// CreateDelivery creates a new delivery in the data store.
	// Returns an error if creation fails.
	CreateDelivery(ctx context.Context, delivery *Delivery) error

	// GetDelivery retrieves a delivery by its ID.
	// Returns ErrDeliveryNotFound if the delivery does not exist.
	GetDelivery(ctx context.Context, id string) (*Delivery, error)

	// UpdateDelivery updates an existing delivery.
	// Returns ErrDeliveryNotFound if the delivery does not exist.
	UpdateDelivery(ctx context.Context, delivery *Delivery) error

	// GetPendingDeliveries retrieves pending deliveries ready for processing.
	// Uses SKIP LOCKED to prevent concurrent workers from processing the same delivery.
	// Returns up to 'limit' deliveries ordered by created_at.
	GetPendingDeliveries(ctx context.Context, limit int) ([]*Delivery, error)

	// ListDeliveries retrieves deliveries matching the given filter.
	// Returns an empty slice if no deliveries match.
	ListDeliveries(ctx context.Context, filter *DeliveryFilter) ([]*Delivery, error)

	// MoveToDeadLetter moves a delivery to the dead letter queue.
	// This is called when a delivery exhausts all retry attempts.
	MoveToDeadLetter(ctx context.Context, deliveryID string, reason string) error

	// DeleteDelivery permanently deletes a delivery and its attempts.
	// This is used for purging dead letter deliveries.
	// Returns ErrDeliveryNotFound if the delivery does not exist.
	DeleteDelivery(ctx context.Context, id string) error

	// Delivery Attempt Operations

	// CreateDeliveryAttempt records a delivery attempt.
	// Returns an error if creation fails.
	CreateDeliveryAttempt(ctx context.Context, attempt *DeliveryAttempt) error

	// GetDeliveryAttempts retrieves all attempts for a delivery.
	// Returns attempts ordered by attempt_number ascending.
	GetDeliveryAttempts(ctx context.Context, deliveryID string) ([]*DeliveryAttempt, error)

	// Idempotency Operations

	// CheckIdempotency checks if an idempotency key exists and is not expired.
	// Returns true if the key exists and is valid, false otherwise.
	CheckIdempotency(ctx context.Context, key string, subscriptionID string) (bool, error)

	// StoreIdempotencyKey stores an idempotency key with an expiration time.
	// Automatically cleans up expired keys on query.
	StoreIdempotencyKey(ctx context.Context, key string, subscriptionID string, expiresAt time.Time) error

	// Circuit Breaker Operations

	// GetCircuitBreakerState retrieves the circuit breaker state for an endpoint.
	// Returns a default closed state if no state exists.
	GetCircuitBreakerState(ctx context.Context, endpoint string) (*CircuitBreakerState, error)

	// UpdateCircuitBreakerState updates the circuit breaker state for an endpoint.
	// Creates a new state if one doesn't exist.
	UpdateCircuitBreakerState(ctx context.Context, state *CircuitBreakerState) error

	// Transaction Support

	// BeginTx begins a new transaction and returns a RepositoryTx.
	// All operations within the transaction must use the returned RepositoryTx.
	// The caller is responsible for calling Commit() or Rollback().
	BeginTx(ctx context.Context) (RepositoryTx, error)

	// Health and Maintenance

	// Ping checks if the repository is accessible and healthy.
	// Returns an error if the connection is not available.
	Ping(ctx context.Context) error

	// Close closes the repository connection and releases resources.
	// After calling Close, no other methods should be called.
	Close() error

	// Maintenance Operations

	// CountDeliveriesByFilter counts deliveries matching the cleanup filter.
	// This is used for dry-run operations before actual deletion.
	CountDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error)

	// DeleteDeliveriesByFilter deletes deliveries matching the cleanup filter.
	// Returns the number of deliveries deleted.
	// Note: Related delivery attempts are automatically deleted via CASCADE.
	DeleteDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error)

	// GetMaintenanceStats retrieves comprehensive statistics for maintenance planning.
	// Returns counts, oldest/newest timestamps, and breakdown by status.
	GetMaintenanceStats(ctx context.Context) (*MaintenanceStats, error)

	// CountExpiredIdempotencyKeys counts idempotency keys that have expired.
	// This is used for dry-run operations before cleanup.
	CountExpiredIdempotencyKeys(ctx context.Context) (int64, error)

	// CleanupExpiredIdempotencyKeys deletes all expired idempotency keys.
	// Returns the number of keys deleted.
	CleanupExpiredIdempotencyKeys(ctx context.Context) (int64, error)
}

// RepositoryTx extends Repository with transaction control methods.
//
// A RepositoryTx is obtained by calling BeginTx() on a Repository.
// All operations within a transaction must use the RepositoryTx instance.
//
// Thread Safety: A RepositoryTx is NOT safe for concurrent use. Each transaction
// should be used by a single goroutine.
//
// Usage:
//
//	tx, err := repo.BeginTx(ctx)
//	if err != nil {
//	    return err
//	}
//	defer tx.Rollback() // Safe to call even after Commit
//
//	if err := tx.CreateSubscription(ctx, sub); err != nil {
//	    return err
//	}
//
//	if err := tx.CreateDelivery(ctx, delivery); err != nil {
//	    return err
//	}
//
//	return tx.Commit()
type RepositoryTx interface {
	Repository

	// Commit commits the transaction.
	// After Commit, the RepositoryTx should not be used.
	// Returns an error if the commit fails.
	Commit() error

	// Rollback rolls back the transaction.
	// It's safe to call Rollback after Commit (it will be a no-op).
	// After Rollback, the RepositoryTx should not be used.
	Rollback() error
}

// Validator defines the validation interface for request types.
//
// All request types (CreateSubscriptionRequest, UpdateSubscriptionRequest, etc.)
// must implement this interface to ensure input validation before processing.
type Validator interface {
	// Validate validates the implementing type's fields.
	// Returns an error if validation fails, with a descriptive message.
	// Returns nil if validation succeeds.
	Validate() error
}
