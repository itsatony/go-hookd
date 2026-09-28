// Package internal provides the core webhook management implementation for go-hookd.
//
// This file implements the RepositoryTx interface using PostgreSQL transactions.
// It provides atomic multi-operation support with commit and rollback semantics.
package hookd

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/itsatony/go-cuserr"
	"github.com/lib/pq"
)

// PostgresRepositoryTx implements the RepositoryTx interface using PostgreSQL transactions.
//
// Thread Safety: PostgresRepositoryTx is NOT safe for concurrent use. Each transaction
// should be used by a single goroutine. Create separate transactions for concurrent operations.
type PostgresRepositoryTx struct {
	tx           *sql.Tx
	db           *sql.DB       // Keep reference for connection pool info
	schemaConfig *SchemaConfig // Schema configuration for table names
}

// =============================================================================
// TRANSACTION CONTROL
// =============================================================================

// Commit commits the transaction.
// After Commit, the RepositoryTx should not be used.
func (r *PostgresRepositoryTx) Commit() error {
	if err := r.tx.Commit(); err != nil {
		if err == sql.ErrTxDone {
			// Transaction already completed (commit or rollback), this is safe
			return nil
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "commit_transaction"),
		)
	}
	return nil
}

// Rollback rolls back the transaction.
// It's safe to call Rollback after Commit (it will be a no-op).
func (r *PostgresRepositoryTx) Rollback() error {
	if err := r.tx.Rollback(); err != nil {
		if err == sql.ErrTxDone {
			// Transaction already completed (commit or rollback), this is safe
			return nil
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "rollback_transaction"),
		)
	}
	return nil
}

// =============================================================================
// SUBSCRIPTION OPERATIONS
// =============================================================================

// CreateSubscription creates a new subscription within the transaction.
func (r *PostgresRepositoryTx) CreateSubscription(ctx context.Context, sub *Subscription) error {
	retryPolicyJSON, err := marshalJSONB(sub.RetryPolicy)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	headersJSON, err := marshalJSONB(sub.Headers)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	metadataJSON, err := marshalJSONB(sub.Metadata)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	query := `
		INSERT INTO subscriptions (
			id, tenant_id, url, secret, event_types, status,
			retry_policy, headers, metadata, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)`

	_, err = r.tx.ExecContext(ctx, query,
		sub.ID,
		sub.TenantID,
		sub.URL,
		sub.Secret,
		pq.Array(sub.EventTypes),
		sub.Status,
		retryPolicyJSON,
		headersJSON,
		metadataJSON,
		sub.CreatedAt,
		sub.UpdatedAt,
	)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return ErrDuplicateSubscription
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "create_subscription_tx"),
		)
	}

	return nil
}

// GetSubscription retrieves a subscription within the transaction.
func (r *PostgresRepositoryTx) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	query := `
		SELECT id, tenant_id, url, secret, event_types, status,
		       retry_policy, headers, metadata, created_at, updated_at
		FROM subscriptions
		WHERE id = $1`

	row := r.tx.QueryRowContext(ctx, query, id)
	sub, err := scanSubscription(row)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSubscriptionNotFound
		}
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_subscription_tx"),
		)
	}

	return sub, nil
}

// GetSubscriptionByTenantAndURL retrieves a subscription by tenant ID and URL within the transaction.
func (r *PostgresRepositoryTx) GetSubscriptionByTenantAndURL(ctx context.Context, tenantID, url string) (*Subscription, error) {
	query := `
		SELECT id, tenant_id, url, secret, event_types, status,
		       retry_policy, headers, metadata, created_at, updated_at
		FROM subscriptions
		WHERE tenant_id = $1 AND url = $2`

	row := r.tx.QueryRowContext(ctx, query, tenantID, url)
	sub, err := scanSubscription(row)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSubscriptionNotFound
		}
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_subscription_by_tenant_url_tx"),
		)
	}

	return sub, nil
}

// UpdateSubscription updates a subscription within the transaction.
func (r *PostgresRepositoryTx) UpdateSubscription(ctx context.Context, sub *Subscription) error {
	retryPolicyJSON, err := marshalJSONB(sub.RetryPolicy)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	headersJSON, err := marshalJSONB(sub.Headers)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	metadataJSON, err := marshalJSONB(sub.Metadata)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	query := `
		UPDATE subscriptions
		SET url = $2,
		    secret = $3,
		    event_types = $4,
		    status = $5,
		    retry_policy = $6,
		    headers = $7,
		    metadata = $8,
		    updated_at = $9
		WHERE id = $1`

	result, err := r.tx.ExecContext(ctx, query,
		sub.ID,
		sub.URL,
		sub.Secret,
		pq.Array(sub.EventTypes),
		sub.Status,
		retryPolicyJSON,
		headersJSON,
		metadataJSON,
		time.Now(),
	)

	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "update_subscription_tx"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	if rowsAffected == 0 {
		return ErrSubscriptionNotFound
	}

	return nil
}

// DeleteSubscription deletes a subscription within the transaction.
func (r *PostgresRepositoryTx) DeleteSubscription(ctx context.Context, id string) error {
	query := `DELETE FROM subscriptions WHERE id = $1`

	result, err := r.tx.ExecContext(ctx, query, id)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "delete_subscription_tx"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	if rowsAffected == 0 {
		return ErrSubscriptionNotFound
	}

	return nil
}

// ListSubscriptions retrieves subscriptions within the transaction.
func (r *PostgresRepositoryTx) ListSubscriptions(ctx context.Context, filter *SubscriptionFilter) ([]*Subscription, error) {
	// ⛔ Defence in depth: refuse a tenant-less scan unless AllTenants is set.
	if err := filter.requireTenantScope(); err != nil {
		return nil, err
	}
	query := `
		SELECT id, tenant_id, url, secret, event_types, status,
		       retry_policy, headers, metadata, created_at, updated_at
		FROM subscriptions
		WHERE 1=1`

	args := []any{}
	argCount := 1

	if filter.TenantID != "" {
		query += fmt.Sprintf(" AND tenant_id = $%d", argCount)
		args = append(args, filter.TenantID)
		argCount++
	}

	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, filter.Status)
		argCount++
	}

	if len(filter.EventTypes) > 0 {
		query += fmt.Sprintf(" AND event_types && $%d", argCount)
		args = append(args, pq.Array(filter.EventTypes))
		argCount++
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filter.Limit)
		argCount++
	}

	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filter.Offset)
	}

	rows, err := r.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "list_subscriptions_tx"),
		)
	}
	defer rows.Close()

	subscriptions := []*Subscription{}
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_subscription_tx"),
			)
		}
		subscriptions = append(subscriptions, sub)
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "list_subscriptions_rows_tx"),
		)
	}

	return subscriptions, nil
}

// =============================================================================
// DELIVERY OPERATIONS
// =============================================================================

// CreateDelivery creates a new delivery within the transaction.
func (r *PostgresRepositoryTx) CreateDelivery(ctx context.Context, delivery *Delivery) error {
	payloadJSON, err := marshalJSONB(delivery.Payload)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	query := `
		INSERT INTO deliveries (
			id, subscription_id, tenant_id, event_type, payload,
			status, attempt_count, max_attempts, next_retry_at,
			completed_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)`

	_, err = r.tx.ExecContext(ctx, query,
		delivery.ID,
		delivery.SubscriptionID,
		delivery.TenantID,
		delivery.EventType,
		payloadJSON,
		delivery.Status,
		delivery.AttemptCount,
		delivery.MaxAttempts,
		delivery.NextRetryAt,
		delivery.CompletedAt,
		delivery.CreatedAt,
	)

	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "create_delivery_tx"),
		)
	}

	return nil
}

// GetDelivery retrieves a delivery within the transaction.
func (r *PostgresRepositoryTx) GetDelivery(ctx context.Context, id string) (*Delivery, error) {
	query := `
		SELECT id, subscription_id, tenant_id, event_type, payload,
		       status, attempt_count, max_attempts, next_retry_at,
		       completed_at, created_at
		FROM deliveries
		WHERE id = $1`

	row := r.tx.QueryRowContext(ctx, query, id)
	delivery, err := scanDelivery(row)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrDeliveryNotFound
		}
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_delivery_tx"),
		)
	}

	return delivery, nil
}

// UpdateDelivery updates a delivery within the transaction.
func (r *PostgresRepositoryTx) UpdateDelivery(ctx context.Context, delivery *Delivery) error {
	payloadJSON, err := marshalJSONB(delivery.Payload)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	query := `
		UPDATE deliveries
		SET subscription_id = $2,
		    tenant_id = $3,
		    event_type = $4,
		    payload = $5,
		    status = $6,
		    attempt_count = $7,
		    max_attempts = $8,
		    next_retry_at = $9,
		    completed_at = $10
		WHERE id = $1`

	result, err := r.tx.ExecContext(ctx, query,
		delivery.ID,
		delivery.SubscriptionID,
		delivery.TenantID,
		delivery.EventType,
		payloadJSON,
		delivery.Status,
		delivery.AttemptCount,
		delivery.MaxAttempts,
		delivery.NextRetryAt,
		delivery.CompletedAt,
	)

	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "update_delivery_tx"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	if rowsAffected == 0 {
		return ErrDeliveryNotFound
	}

	return nil
}

// GetPendingDeliveries retrieves pending deliveries within the transaction.
// Uses SKIP LOCKED to prevent concurrent workers from processing the same delivery.
func (r *PostgresRepositoryTx) GetPendingDeliveries(ctx context.Context, limit int) ([]*Delivery, error) {
	query := `
		SELECT id, subscription_id, tenant_id, event_type, payload,
		       status, attempt_count, max_attempts, next_retry_at,
		       completed_at, created_at
		FROM deliveries
		WHERE status = $1
		  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY
		  COALESCE(next_retry_at, created_at),
		  created_at
		LIMIT $2
		FOR UPDATE SKIP LOCKED`

	rows, err := r.tx.QueryContext(ctx, query, DeliveryStatusPending, limit)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_pending_deliveries_tx"),
		)
	}
	defer rows.Close()

	deliveries := []*Delivery{}
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_delivery_tx"),
			)
		}
		deliveries = append(deliveries, delivery)
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_pending_deliveries_rows_tx"),
		)
	}

	return deliveries, nil
}

// ListDeliveries retrieves deliveries matching the given filter within the transaction.
func (r *PostgresRepositoryTx) ListDeliveries(ctx context.Context, filter *DeliveryFilter) ([]*Delivery, error) {
	// ⛔ Defence in depth: refuse a tenant-less scan unless AllTenants is set.
	if err := filter.requireTenantScope(); err != nil {
		return nil, err
	}
	query := `
		SELECT id, subscription_id, tenant_id, event_type, payload,
		       status, attempt_count, max_attempts, next_retry_at,
		       completed_at, created_at
		FROM deliveries
		WHERE 1=1`

	args := []any{}
	argCount := 1

	// Apply filters
	if filter.TenantID != "" {
		query += fmt.Sprintf(" AND tenant_id = $%d", argCount)
		args = append(args, filter.TenantID)
		argCount++
	}

	if filter.SubscriptionID != nil && *filter.SubscriptionID != "" {
		query += fmt.Sprintf(" AND subscription_id = $%d", argCount)
		args = append(args, *filter.SubscriptionID)
		argCount++
	}

	if filter.Status != nil && *filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, *filter.Status)
		argCount++
	}

	if filter.EventType != nil && *filter.EventType != "" {
		query += fmt.Sprintf(" AND event_type = $%d", argCount)
		args = append(args, *filter.EventType)
		argCount++
	}

	// Order by created_at descending (newest first)
	query += " ORDER BY created_at DESC"

	// Apply limit if specified
	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, filter.Limit)
		argCount++
	}

	// Apply offset if specified
	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, filter.Offset)
	}

	rows, err := r.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "list_deliveries_tx"),
		)
	}
	defer rows.Close()

	deliveries := []*Delivery{}
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_delivery_tx"),
			)
		}
		deliveries = append(deliveries, delivery)
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "list_deliveries_rows_tx"),
		)
	}

	return deliveries, nil
}

// MoveToDeadLetter moves a delivery to the dead letter queue within the transaction.
func (r *PostgresRepositoryTx) MoveToDeadLetter(ctx context.Context, deliveryID string, reason string) error {
	query := `
		UPDATE deliveries
		SET status = $2,
		    completed_at = $3
		WHERE id = $1`

	result, err := r.tx.ExecContext(ctx, query,
		deliveryID,
		DeliveryStatusDeadLetter,
		time.Now(),
	)

	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "move_to_dead_letter_tx"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	if rowsAffected == 0 {
		return ErrDeliveryNotFound
	}

	return nil
}

// DeleteDelivery permanently deletes a delivery and its attempts within the transaction.
func (r *PostgresRepositoryTx) DeleteDelivery(ctx context.Context, id string) error {
	// First delete all delivery attempts (foreign key constraint)
	_, err := r.tx.ExecContext(ctx,
		`DELETE FROM delivery_attempts WHERE delivery_id = $1`,
		id,
	)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "delete_delivery_attempts_tx"),
			cuserr.WithMetadata("delivery_id", id),
		)
	}

	// Then delete the delivery
	result, err := r.tx.ExecContext(ctx,
		`DELETE FROM deliveries WHERE id = $1`,
		id,
	)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "delete_delivery_tx"),
			cuserr.WithMetadata("delivery_id", id),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	if rowsAffected == 0 {
		return ErrDeliveryNotFound
	}

	return nil
}

// =============================================================================
// DELIVERY ATTEMPT OPERATIONS
// =============================================================================

// CreateDeliveryAttempt records a delivery attempt within the transaction.
func (r *PostgresRepositoryTx) CreateDeliveryAttempt(ctx context.Context, attempt *DeliveryAttempt) error {
	responseHeadersJSON, err := marshalJSONB(attempt.ResponseHeaders)
	if err != nil {
		return cuserr.NewInternalError("database", err)
	}

	query := `
		INSERT INTO delivery_attempts (
			id, delivery_id, attempt_number, status_code,
			response_body, response_headers, error, attempted_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)`

	_, err = r.tx.ExecContext(ctx, query,
		attempt.ID,
		attempt.DeliveryID,
		attempt.AttemptNumber,
		attempt.StatusCode,
		attempt.ResponseBody,
		responseHeadersJSON,
		attempt.Error,
		attempt.AttemptedAt,
	)

	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return cuserr.NewConflictError("delivery_attempt", "attempt_number",
				fmt.Sprintf("attempt %d already exists for delivery %s", attempt.AttemptNumber, attempt.DeliveryID))
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "create_delivery_attempt_tx"),
		)
	}

	return nil
}

// GetDeliveryAttempts retrieves all attempts for a delivery within the transaction.
func (r *PostgresRepositoryTx) GetDeliveryAttempts(ctx context.Context, deliveryID string) ([]*DeliveryAttempt, error) {
	query := `
		SELECT id, delivery_id, attempt_number, status_code,
		       response_body, response_headers, error, attempted_at
		FROM delivery_attempts
		WHERE delivery_id = $1
		ORDER BY attempt_number ASC`

	rows, err := r.tx.QueryContext(ctx, query, deliveryID)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_delivery_attempts_tx"),
		)
	}
	defer rows.Close()

	attempts := []*DeliveryAttempt{}
	for rows.Next() {
		attempt, err := scanDeliveryAttempt(rows)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_delivery_attempt_tx"),
			)
		}
		attempts = append(attempts, attempt)
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_delivery_attempts_rows_tx"),
		)
	}

	return attempts, nil
}

// =============================================================================
// IDEMPOTENCY OPERATIONS
// =============================================================================

// CheckIdempotency checks if an idempotency key exists within the transaction.
func (r *PostgresRepositoryTx) CheckIdempotency(ctx context.Context, key string, subscriptionID string) (bool, error) {
	query := `
		SELECT COUNT(*)
		FROM idempotency_store
		WHERE idempotency_key = $1
		  AND subscription_id = $2
		  AND expires_at > NOW()`

	var count int
	err := r.tx.QueryRowContext(ctx, query, key, subscriptionID).Scan(&count)
	if err != nil {
		return false, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "check_idempotency_tx"),
		)
	}

	return count > 0, nil
}

// StoreIdempotencyKey stores an idempotency key within the transaction.
func (r *PostgresRepositoryTx) StoreIdempotencyKey(ctx context.Context, key string, subscriptionID string, expiresAt time.Time) error {
	query := `
		INSERT INTO idempotency_store (
			idempotency_key, subscription_id, expires_at, created_at
		) VALUES (
			$1, $2, $3, $4
		)
		ON CONFLICT (idempotency_key, subscription_id) DO UPDATE
		SET expires_at = EXCLUDED.expires_at`

	_, err := r.tx.ExecContext(ctx, query,
		key,
		subscriptionID,
		expiresAt,
		time.Now(),
	)

	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "store_idempotency_key_tx"),
		)
	}

	return nil
}

// =============================================================================
// CIRCUIT BREAKER OPERATIONS
// =============================================================================

// GetCircuitBreakerState retrieves the circuit breaker state within the transaction.
func (r *PostgresRepositoryTx) GetCircuitBreakerState(ctx context.Context, endpoint string) (*CircuitBreakerState, error) {
	query := `
		SELECT endpoint, state, failure_count, success_count,
		       last_failure, opened_at, next_retry_at, updated_at
		FROM circuit_breaker_state
		WHERE endpoint = $1`

	row := r.tx.QueryRowContext(ctx, query, endpoint)
	state, err := scanCircuitBreakerState(row)

	if err != nil {
		if err == sql.ErrNoRows {
			return &CircuitBreakerState{
				Endpoint:     endpoint,
				State:        CircuitBreakerStateClosed,
				FailureCount: 0,
				SuccessCount: 0,
			}, nil
		}
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_circuit_breaker_state_tx"),
		)
	}

	return state, nil
}

// UpdateCircuitBreakerState updates the circuit breaker state within the transaction.
func (r *PostgresRepositoryTx) UpdateCircuitBreakerState(ctx context.Context, state *CircuitBreakerState) error {
	query := `
		INSERT INTO circuit_breaker_state (
			endpoint, state, failure_count, success_count,
			last_failure, opened_at, next_retry_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
		ON CONFLICT (endpoint) DO UPDATE
		SET state = EXCLUDED.state,
		    failure_count = EXCLUDED.failure_count,
		    success_count = EXCLUDED.success_count,
		    last_failure = EXCLUDED.last_failure,
		    opened_at = EXCLUDED.opened_at,
		    next_retry_at = EXCLUDED.next_retry_at,
		    updated_at = EXCLUDED.updated_at`

	_, err := r.tx.ExecContext(ctx, query,
		state.Endpoint,
		state.State,
		state.FailureCount,
		state.SuccessCount,
		state.LastFailure,
		state.OpenedAt,
		state.NextRetryAt,
		time.Now(),
	)

	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "update_circuit_breaker_state_tx"),
		)
	}

	return nil
}

// =============================================================================
// NESTED TRANSACTIONS (NOT SUPPORTED)
// =============================================================================

// BeginTx is not supported within a transaction.
// Attempting to start a nested transaction returns an error.
func (r *PostgresRepositoryTx) BeginTx(ctx context.Context) (RepositoryTx, error) {
	return nil, cuserr.NewValidationError("transaction", "nested transactions are not supported")
}

// =============================================================================
// HEALTH AND MAINTENANCE
// =============================================================================

// Ping checks if the underlying database connection is healthy.
// Note: This pings the connection pool, not the transaction itself.
func (r *PostgresRepositoryTx) Ping(ctx context.Context) error {
	if err := r.db.PingContext(ctx); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "ping_tx"),
		)
	}
	return nil
}

// Close is a no-op for transactions.
// Transactions should be committed or rolled back, not closed.
// This method exists to satisfy the Repository interface.
func (r *PostgresRepositoryTx) Close() error {
	// No-op: transactions are committed or rolled back, not closed
	return nil
}

// =============================================================================
// MAINTENANCE OPERATIONS
// =============================================================================

// CountDeliveriesByFilter counts deliveries matching the cleanup filter within the transaction.
func (r *PostgresRepositoryTx) CountDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error) {
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE 1=1`, r.schemaConfig.TableDeliveries())
	args := []any{}
	argCount := 1

	if filter.CreatedBefore != nil {
		query += fmt.Sprintf(" AND created_at < $%d", argCount)
		args = append(args, *filter.CreatedBefore)
		argCount++
	}

	if filter.CreatedAfter != nil {
		query += fmt.Sprintf(" AND created_at > $%d", argCount)
		args = append(args, *filter.CreatedAfter)
		argCount++
	}

	if filter.Status != nil && *filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, *filter.Status)
		argCount++
	}

	if filter.TenantID != "" {
		query += fmt.Sprintf(" AND tenant_id = $%d", argCount)
		args = append(args, filter.TenantID)
		argCount++
	}

	if filter.SubscriptionID != nil && *filter.SubscriptionID != "" {
		query += fmt.Sprintf(" AND subscription_id = $%d", argCount)
		args = append(args, *filter.SubscriptionID)
		argCount++
	}

	if filter.EventType != nil && *filter.EventType != "" {
		query += fmt.Sprintf(" AND event_type = $%d", argCount)
		args = append(args, *filter.EventType)
	}

	var count int64
	err := r.tx.QueryRowContext(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "count_deliveries_by_filter_tx"),
		)
	}

	return count, nil
}

// DeleteDeliveriesByFilter deletes deliveries matching the cleanup filter within the transaction.
func (r *PostgresRepositoryTx) DeleteDeliveriesByFilter(ctx context.Context, filter *CleanupFilter) (int64, error) {
	query := fmt.Sprintf(`DELETE FROM %s WHERE 1=1`, r.schemaConfig.TableDeliveries())
	args := []any{}
	argCount := 1

	if filter.CreatedBefore != nil {
		query += fmt.Sprintf(" AND created_at < $%d", argCount)
		args = append(args, *filter.CreatedBefore)
		argCount++
	}

	if filter.CreatedAfter != nil {
		query += fmt.Sprintf(" AND created_at > $%d", argCount)
		args = append(args, *filter.CreatedAfter)
		argCount++
	}

	if filter.Status != nil && *filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", argCount)
		args = append(args, *filter.Status)
		argCount++
	}

	if filter.TenantID != "" {
		query += fmt.Sprintf(" AND tenant_id = $%d", argCount)
		args = append(args, filter.TenantID)
		argCount++
	}

	if filter.SubscriptionID != nil && *filter.SubscriptionID != "" {
		query += fmt.Sprintf(" AND subscription_id = $%d", argCount)
		args = append(args, *filter.SubscriptionID)
		argCount++
	}

	if filter.EventType != nil && *filter.EventType != "" {
		query += fmt.Sprintf(" AND event_type = $%d", argCount)
		args = append(args, *filter.EventType)
	}

	result, err := r.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "delete_deliveries_by_filter_tx"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "delete_deliveries_rows_affected_tx"),
		)
	}

	return rowsAffected, nil
}

// GetMaintenanceStats retrieves comprehensive statistics within the transaction.
func (r *PostgresRepositoryTx) GetMaintenanceStats(ctx context.Context) (*MaintenanceStats, error) {
	stats := &MaintenanceStats{
		DeliveriesByStatus: make(map[string]int64),
		AsOf:               time.Now(),
	}

	// Get total deliveries and time range
	deliveryStatsQuery := fmt.Sprintf(`
		SELECT
			COUNT(*) as total,
			MIN(created_at) as oldest,
			MAX(created_at) as newest
		FROM %s`, r.schemaConfig.TableDeliveries())

	var oldest, newest *time.Time
	err := r.tx.QueryRowContext(ctx, deliveryStatsQuery).Scan(
		&stats.TotalDeliveries,
		&oldest,
		&newest,
	)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_maintenance_stats_deliveries_tx"),
		)
	}
	stats.OldestDeliveryAt = oldest
	stats.NewestDeliveryAt = newest

	// Get breakdown by status
	statusQuery := fmt.Sprintf(`
		SELECT status, COUNT(*) as count
		FROM %s
		GROUP BY status`, r.schemaConfig.TableDeliveries())

	rows, err := r.tx.QueryContext(ctx, statusQuery)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_maintenance_stats_by_status_tx"),
		)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_status_count_tx"),
			)
		}
		stats.DeliveriesByStatus[status] = count
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_maintenance_stats_status_rows_tx"),
		)
	}

	// Get total delivery attempts
	attemptsQuery := fmt.Sprintf(`SELECT COUNT(*) FROM %s`, r.schemaConfig.TableDeliveryAttempts())
	err = r.tx.QueryRowContext(ctx, attemptsQuery).Scan(&stats.TotalDeliveryAttempts)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_maintenance_stats_attempts_tx"),
		)
	}

	// Get idempotency key counts
	idempotencyQuery := fmt.Sprintf(`
		SELECT
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE expires_at < NOW()) as expired
		FROM %s`, r.schemaConfig.TableIdempotencyStore())

	err = r.tx.QueryRowContext(ctx, idempotencyQuery).Scan(
		&stats.IdempotencyKeys,
		&stats.ExpiredIdempotencyKeys,
	)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_maintenance_stats_idempotency_tx"),
		)
	}

	return stats, nil
}

// CountExpiredIdempotencyKeys counts expired idempotency keys within the transaction.
func (r *PostgresRepositoryTx) CountExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE expires_at < NOW()`, r.schemaConfig.TableIdempotencyStore())

	var count int64
	err := r.tx.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "count_expired_idempotency_keys_tx"),
		)
	}

	return count, nil
}

// CleanupExpiredIdempotencyKeys deletes expired idempotency keys within the transaction.
func (r *PostgresRepositoryTx) CleanupExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	query := fmt.Sprintf(`DELETE FROM %s WHERE expires_at < NOW()`, r.schemaConfig.TableIdempotencyStore())

	result, err := r.tx.ExecContext(ctx, query)
	if err != nil {
		return 0, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "cleanup_expired_idempotency_keys_tx"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "cleanup_idempotency_rows_affected_tx"),
		)
	}

	return rowsAffected, nil
}
