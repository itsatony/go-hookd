// Package internal provides the core webhook management implementation for go-hookd.
//
// This file implements the Repository interface using PostgreSQL as the backend.
// All operations are thread-safe and use parameterized queries to prevent SQL injection.
package hookd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/itsatony/go-cuserr"
	"github.com/lib/pq"
)

// PostgresRepository implements the Repository interface using PostgreSQL.
//
// Thread Safety: PostgresRepository is safe for concurrent use by multiple goroutines.
// The underlying sql.DB connection pool handles concurrent access automatically.
type PostgresRepository struct {
	db *sql.DB
}

// NewPostgresRepository creates a new PostgreSQL repository instance.
//
// The connectionString should be in the format:
// postgresql://user:password@host:port/database?sslmode=disable
//
// Returns an error if the connection cannot be established or the database
// cannot be pinged.
func NewPostgresRepository(connectionString string) (*PostgresRepository, error) {
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "open"),
		)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		// Close database connection if ping fails
		// Ignore close error as we're already returning a connection error
		_ = db.Close()
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "ping"),
		)
	}

	return &PostgresRepository{db: db}, nil
}

// =============================================================================
// HELPER FUNCTIONS
// =============================================================================

// marshalJSONB marshals a value to JSONB format for PostgreSQL.
// Returns nil for nil input to handle nullable JSONB columns.
func marshalJSONB(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "jsonb_marshal", err,
			cuserr.WithMetadata("operation", "marshal"),
		)
	}
	return data, nil
}

// unmarshalJSONB unmarshals JSONB data from PostgreSQL into the target.
// Handles NULL values by leaving target unchanged.
func unmarshalJSONB(data []byte, target any) error {
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return cuserr.NewExternalError("database", "jsonb_unmarshal", err,
			cuserr.WithMetadata("operation", "unmarshal"),
		)
	}
	return nil
}

// scanSubscription scans a database row into a Subscription struct.
// Handles JSONB fields (retry_policy, headers, metadata) and TEXT[] arrays (event_types).
func scanSubscription(scanner interface {
	Scan(dest ...any) error
}) (*Subscription, error) {
	var sub Subscription
	var retryPolicyJSON []byte
	var headersJSON []byte
	var metadataJSON []byte
	var eventTypes pq.StringArray

	err := scanner.Scan(
		&sub.ID,
		&sub.TenantID,
		&sub.URL,
		&sub.Secret,
		&eventTypes,
		&sub.Status,
		&retryPolicyJSON,
		&headersJSON,
		&metadataJSON,
		&sub.CreatedAt,
		&sub.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Unmarshal JSONB fields
	sub.EventTypes = []string(eventTypes)

	if len(retryPolicyJSON) > 0 {
		sub.RetryPolicy = &RetryPolicy{}
		if err := unmarshalJSONB(retryPolicyJSON, sub.RetryPolicy); err != nil {
			return nil, err
		}
	}

	if len(headersJSON) > 0 {
		sub.Headers = make(map[string]string)
		if err := unmarshalJSONB(headersJSON, &sub.Headers); err != nil {
			return nil, err
		}
	}

	if len(metadataJSON) > 0 {
		sub.Metadata = make(map[string]any)
		if err := unmarshalJSONB(metadataJSON, &sub.Metadata); err != nil {
			return nil, err
		}
	}

	return &sub, nil
}

// scanDelivery scans a database row into a Delivery struct.
// Handles JSONB payload field and nullable timestamps.
func scanDelivery(scanner interface {
	Scan(dest ...any) error
}) (*Delivery, error) {
	var dlv Delivery
	var payloadJSON []byte
	var nextRetryAt sql.NullTime
	var completedAt sql.NullTime

	err := scanner.Scan(
		&dlv.ID,
		&dlv.SubscriptionID,
		&dlv.TenantID,
		&dlv.EventType,
		&payloadJSON,
		&dlv.Status,
		&dlv.AttemptCount,
		&dlv.MaxAttempts,
		&nextRetryAt,
		&completedAt,
		&dlv.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Unmarshal payload JSONB
	if len(payloadJSON) > 0 {
		dlv.Payload = make(map[string]any)
		if err := unmarshalJSONB(payloadJSON, &dlv.Payload); err != nil {
			return nil, err
		}
	}

	// Handle nullable timestamps
	if nextRetryAt.Valid {
		dlv.NextRetryAt = &nextRetryAt.Time
	}
	if completedAt.Valid {
		dlv.CompletedAt = &completedAt.Time
	}

	return &dlv, nil
}

// scanDeliveryAttempt scans a database row into a DeliveryAttempt struct.
// Handles JSONB response_headers field.
func scanDeliveryAttempt(scanner interface {
	Scan(dest ...any) error
}) (*DeliveryAttempt, error) {
	var att DeliveryAttempt
	var responseHeadersJSON []byte
	var responseBody sql.NullString
	var errorMsg sql.NullString

	err := scanner.Scan(
		&att.ID,
		&att.DeliveryID,
		&att.AttemptNumber,
		&att.StatusCode,
		&responseBody,
		&responseHeadersJSON,
		&errorMsg,
		&att.AttemptedAt,
	)
	if err != nil {
		return nil, err
	}

	// Handle nullable strings
	if responseBody.Valid {
		att.ResponseBody = responseBody.String
	}
	if errorMsg.Valid {
		att.Error = errorMsg.String
	}

	// Unmarshal response headers JSONB
	if len(responseHeadersJSON) > 0 {
		att.ResponseHeaders = make(map[string]string)
		if err := unmarshalJSONB(responseHeadersJSON, &att.ResponseHeaders); err != nil {
			return nil, err
		}
	}

	return &att, nil
}

// scanCircuitBreakerState scans a database row into a CircuitBreakerState struct.
// Handles nullable timestamps.
func scanCircuitBreakerState(scanner interface {
	Scan(dest ...any) error
}) (*CircuitBreakerState, error) {
	var state CircuitBreakerState
	var lastFailure sql.NullTime
	var openedAt sql.NullTime
	var nextRetryAt sql.NullTime
	var updatedAt time.Time

	err := scanner.Scan(
		&state.Endpoint,
		&state.State,
		&state.FailureCount,
		&state.SuccessCount,
		&lastFailure,
		&openedAt,
		&nextRetryAt,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}

	// Handle nullable timestamps
	if lastFailure.Valid {
		state.LastFailure = lastFailure.Time
	}
	if openedAt.Valid {
		state.OpenedAt = openedAt.Time
	}
	if nextRetryAt.Valid {
		state.NextRetryAt = nextRetryAt.Time
	}

	return &state, nil
}

// =============================================================================
// SUBSCRIPTION OPERATIONS
// =============================================================================

// CreateSubscription creates a new subscription in the database.
// Returns ErrSubscriptionExists if a subscription with the same tenant_id and url already exists.
func (r *PostgresRepository) CreateSubscription(ctx context.Context, sub *Subscription) error {
	// Marshal JSONB fields
	retryPolicyJSON, err := marshalJSONB(sub.RetryPolicy)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
	}

	headersJSON, err := marshalJSONB(sub.Headers)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
	}

	metadataJSON, err := marshalJSONB(sub.Metadata)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
	}

	query := `
		INSERT INTO subscriptions (
			id, tenant_id, url, secret, event_types, status,
			retry_policy, headers, metadata, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)`

	_, err = r.db.ExecContext(ctx, query,
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
		// Check for unique constraint violation (duplicate tenant_id + url)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return ErrDuplicateSubscription
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "create_subscription"),
		)
	}

	return nil
}

// GetSubscription retrieves a subscription by its ID.
// Returns ErrSubscriptionNotFound if the subscription does not exist.
func (r *PostgresRepository) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	query := `
		SELECT id, tenant_id, url, secret, event_types, status,
		       retry_policy, headers, metadata, created_at, updated_at
		FROM subscriptions
		WHERE id = $1`

	row := r.db.QueryRowContext(ctx, query, id)
	sub, err := scanSubscription(row)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSubscriptionNotFound
		}
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_subscription"),
		)
	}

	return sub, nil
}

// GetSubscriptionByTenantAndURL retrieves a subscription by tenant ID and URL.
// Returns ErrSubscriptionNotFound if no matching subscription exists.
func (r *PostgresRepository) GetSubscriptionByTenantAndURL(ctx context.Context, tenantID, url string) (*Subscription, error) {
	query := `
		SELECT id, tenant_id, url, secret, event_types, status,
		       retry_policy, headers, metadata, created_at, updated_at
		FROM subscriptions
		WHERE tenant_id = $1 AND url = $2`

	row := r.db.QueryRowContext(ctx, query, tenantID, url)
	sub, err := scanSubscription(row)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSubscriptionNotFound
		}
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_subscription_by_tenant_url"),
		)
	}

	return sub, nil
}

// UpdateSubscription updates an existing subscription.
// Returns ErrSubscriptionNotFound if the subscription does not exist.
func (r *PostgresRepository) UpdateSubscription(ctx context.Context, sub *Subscription) error {
	// Marshal JSONB fields
	retryPolicyJSON, err := marshalJSONB(sub.RetryPolicy)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
	}

	headersJSON, err := marshalJSONB(sub.Headers)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
	}

	metadataJSON, err := marshalJSONB(sub.Metadata)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
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

	result, err := r.db.ExecContext(ctx, query,
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
			cuserr.WithMetadata("operation", "update_subscription"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "rows_affected"),
		)
	}

	if rowsAffected == 0 {
		return ErrSubscriptionNotFound
	}

	return nil
}

// DeleteSubscription deletes a subscription by its ID.
// Returns ErrSubscriptionNotFound if the subscription does not exist.
// Cascades to delete all related deliveries and attempts.
func (r *PostgresRepository) DeleteSubscription(ctx context.Context, id string) error {
	query := `DELETE FROM subscriptions WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "delete_subscription"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "rows_affected"),
		)
	}

	if rowsAffected == 0 {
		return ErrSubscriptionNotFound
	}

	return nil
}

// ListSubscriptions retrieves subscriptions matching the given filter.
// Returns an empty slice if no subscriptions match.
func (r *PostgresRepository) ListSubscriptions(ctx context.Context, filter *SubscriptionFilter) ([]*Subscription, error) {
	query := `
		SELECT id, tenant_id, url, secret, event_types, status,
		       retry_policy, headers, metadata, created_at, updated_at
		FROM subscriptions
		WHERE 1=1`

	args := []any{}
	argCount := 1

	// Apply filters
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

	// Order by created_at descending
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

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "list_subscriptions"),
		)
	}
	defer rows.Close()

	subscriptions := []*Subscription{}
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_subscription"),
			)
		}
		subscriptions = append(subscriptions, sub)
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "list_subscriptions_rows"),
		)
	}

	return subscriptions, nil
}

// =============================================================================
// DELIVERY OPERATIONS
// =============================================================================

// CreateDelivery creates a new delivery in the database.
func (r *PostgresRepository) CreateDelivery(ctx context.Context, delivery *Delivery) error {
	// Marshal payload JSONB
	payloadJSON, err := marshalJSONB(delivery.Payload)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
	}

	query := `
		INSERT INTO deliveries (
			id, subscription_id, tenant_id, event_type, payload,
			status, attempt_count, max_attempts, next_retry_at,
			completed_at, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)`

	_, err = r.db.ExecContext(ctx, query,
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
			cuserr.WithMetadata("operation", "create_delivery"),
		)
	}

	return nil
}

// GetDelivery retrieves a delivery by its ID.
// Returns ErrDeliveryNotFound if the delivery does not exist.
func (r *PostgresRepository) GetDelivery(ctx context.Context, id string) (*Delivery, error) {
	query := `
		SELECT id, subscription_id, tenant_id, event_type, payload,
		       status, attempt_count, max_attempts, next_retry_at,
		       completed_at, created_at
		FROM deliveries
		WHERE id = $1`

	row := r.db.QueryRowContext(ctx, query, id)
	delivery, err := scanDelivery(row)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrDeliveryNotFound
		}
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_delivery"),
		)
	}

	return delivery, nil
}

// UpdateDelivery updates an existing delivery.
// Returns ErrDeliveryNotFound if the delivery does not exist.
func (r *PostgresRepository) UpdateDelivery(ctx context.Context, delivery *Delivery) error {
	// Marshal payload JSONB
	payloadJSON, err := marshalJSONB(delivery.Payload)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
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

	result, err := r.db.ExecContext(ctx, query,
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
			cuserr.WithMetadata("operation", "update_delivery"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "rows_affected"),
		)
	}

	if rowsAffected == 0 {
		return ErrDeliveryNotFound
	}

	return nil
}

// GetPendingDeliveries retrieves pending deliveries ready for processing.
// Uses SKIP LOCKED to prevent concurrent workers from processing the same delivery.
// Returns up to 'limit' deliveries ordered by next_retry_at, then created_at.
func (r *PostgresRepository) GetPendingDeliveries(ctx context.Context, limit int) ([]*Delivery, error) {
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

	rows, err := r.db.QueryContext(ctx, query, DeliveryStatusPending, limit)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_pending_deliveries"),
		)
	}
	defer rows.Close()

	deliveries := []*Delivery{}
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_delivery"),
			)
		}
		deliveries = append(deliveries, delivery)
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_pending_deliveries_rows"),
		)
	}

	return deliveries, nil
}

// ListDeliveries retrieves deliveries matching the given filter.
// Returns an empty slice if no deliveries match.
func (r *PostgresRepository) ListDeliveries(ctx context.Context, filter *DeliveryFilter) ([]*Delivery, error) {
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

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "list_deliveries"),
		)
	}
	defer rows.Close()

	deliveries := []*Delivery{}
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_delivery"),
			)
		}
		deliveries = append(deliveries, delivery)
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "list_deliveries_rows"),
		)
	}

	return deliveries, nil
}

// MoveToDeadLetter moves a delivery to the dead letter queue.
// This is called when a delivery exhausts all retry attempts.
func (r *PostgresRepository) MoveToDeadLetter(ctx context.Context, deliveryID string, reason string) error {
	query := `
		UPDATE deliveries
		SET status = $2,
		    completed_at = $3
		WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query,
		deliveryID,
		DeliveryStatusDeadLetter,
		time.Now(),
	)

	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "move_to_dead_letter"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "rows_affected"),
		)
	}

	if rowsAffected == 0 {
		return ErrDeliveryNotFound
	}

	return nil
}

// DeleteDelivery permanently deletes a delivery and its attempts.
// This is used for purging dead letter deliveries.
func (r *PostgresRepository) DeleteDelivery(ctx context.Context, id string) error {
	// Use a transaction to ensure both the delivery and its attempts are deleted atomically
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "begin_tx"),
		)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is a no-op if already committed

	// Delete attempts first (foreign key constraint)
	_, err = tx.ExecContext(ctx, "DELETE FROM delivery_attempts WHERE delivery_id = $1", id)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "delete_attempts"),
		)
	}

	// Delete the delivery
	result, err := tx.ExecContext(ctx, "DELETE FROM deliveries WHERE id = $1", id)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "delete_delivery"),
		)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "rows_affected"),
		)
	}

	if rowsAffected == 0 {
		return ErrDeliveryNotFound
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "commit"),
		)
	}

	return nil
}

// =============================================================================
// DELIVERY ATTEMPT OPERATIONS
// =============================================================================

// CreateDeliveryAttempt records a delivery attempt in the database.
func (r *PostgresRepository) CreateDeliveryAttempt(ctx context.Context, attempt *DeliveryAttempt) error {
	// Marshal response headers JSONB
	responseHeadersJSON, err := marshalJSONB(attempt.ResponseHeaders)
	if err != nil {
		return err // Already wrapped as ExternalError by marshalJSONB
	}

	query := `
		INSERT INTO delivery_attempts (
			id, delivery_id, attempt_number, status_code,
			response_body, response_headers, error, attempted_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)`

	_, err = r.db.ExecContext(ctx, query,
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
		// Check for unique constraint violation (duplicate delivery_id + attempt_number)
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return cuserr.NewConflictError("delivery_attempt", "attempt_number",
				fmt.Sprintf("attempt %d already exists for delivery %s", attempt.AttemptNumber, attempt.DeliveryID))
		}
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "create_delivery_attempt"),
		)
	}

	return nil
}

// GetDeliveryAttempts retrieves all attempts for a delivery.
// Returns attempts ordered by attempt_number ascending.
func (r *PostgresRepository) GetDeliveryAttempts(ctx context.Context, deliveryID string) ([]*DeliveryAttempt, error) {
	query := `
		SELECT id, delivery_id, attempt_number, status_code,
		       response_body, response_headers, error, attempted_at
		FROM delivery_attempts
		WHERE delivery_id = $1
		ORDER BY attempt_number ASC`

	rows, err := r.db.QueryContext(ctx, query, deliveryID)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_delivery_attempts"),
		)
	}
	defer rows.Close()

	attempts := []*DeliveryAttempt{}
	for rows.Next() {
		attempt, err := scanDeliveryAttempt(rows)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_delivery_attempt"),
			)
		}
		attempts = append(attempts, attempt)
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_delivery_attempts_rows"),
		)
	}

	return attempts, nil
}

// =============================================================================
// IDEMPOTENCY OPERATIONS
// =============================================================================

// CheckIdempotency checks if an idempotency key exists and is not expired.
// Returns true if the key exists and is valid, false otherwise.
func (r *PostgresRepository) CheckIdempotency(ctx context.Context, key string, subscriptionID string) (bool, error) {
	query := `
		SELECT COUNT(*)
		FROM idempotency_store
		WHERE idempotency_key = $1
		  AND subscription_id = $2
		  AND expires_at > NOW()`

	var count int
	err := r.db.QueryRowContext(ctx, query, key, subscriptionID).Scan(&count)
	if err != nil {
		return false, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "check_idempotency"),
		)
	}

	return count > 0, nil
}

// StoreIdempotencyKey stores an idempotency key with an expiration time.
// Automatically cleans up expired keys on query.
func (r *PostgresRepository) StoreIdempotencyKey(ctx context.Context, key string, subscriptionID string, expiresAt time.Time) error {
	query := `
		INSERT INTO idempotency_store (
			idempotency_key, subscription_id, expires_at, created_at
		) VALUES (
			$1, $2, $3, $4
		)
		ON CONFLICT (idempotency_key, subscription_id) DO UPDATE
		SET expires_at = EXCLUDED.expires_at`

	_, err := r.db.ExecContext(ctx, query,
		key,
		subscriptionID,
		expiresAt,
		time.Now(),
	)

	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "store_idempotency_key"),
		)
	}

	return nil
}

// =============================================================================
// CIRCUIT BREAKER OPERATIONS
// =============================================================================

// GetCircuitBreakerState retrieves the circuit breaker state for an endpoint.
// Returns a default closed state if no state exists.
func (r *PostgresRepository) GetCircuitBreakerState(ctx context.Context, endpoint string) (*CircuitBreakerState, error) {
	query := `
		SELECT endpoint, state, failure_count, success_count,
		       last_failure, opened_at, next_retry_at, updated_at
		FROM circuit_breaker_state
		WHERE endpoint = $1`

	row := r.db.QueryRowContext(ctx, query, endpoint)
	state, err := scanCircuitBreakerState(row)

	if err != nil {
		if err == sql.ErrNoRows {
			// Return default closed state
			return &CircuitBreakerState{
				Endpoint:     endpoint,
				State:        CircuitBreakerStateClosed,
				FailureCount: 0,
				SuccessCount: 0,
			}, nil
		}
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_circuit_breaker_state"),
		)
	}

	return state, nil
}

// UpdateCircuitBreakerState updates the circuit breaker state for an endpoint.
// Creates a new state if one doesn't exist (UPSERT).
func (r *PostgresRepository) UpdateCircuitBreakerState(ctx context.Context, state *CircuitBreakerState) error {
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

	_, err := r.db.ExecContext(ctx, query,
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
			cuserr.WithMetadata("operation", "update_circuit_breaker_state"),
		)
	}

	return nil
}

// =============================================================================
// TRANSACTION SUPPORT
// =============================================================================

// BeginTx begins a new transaction and returns a RepositoryTx.
// The caller is responsible for calling Commit() or Rollback().
func (r *PostgresRepository) BeginTx(ctx context.Context) (RepositoryTx, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "begin_transaction"),
		)
	}

	return &PostgresRepositoryTx{
		tx: tx,
		db: r.db, // Keep reference for connection pool info
	}, nil
}

// =============================================================================
// HEALTH AND MAINTENANCE
// =============================================================================

// Ping checks if the repository is accessible and healthy.
// Returns an error if the connection is not available.
func (r *PostgresRepository) Ping(ctx context.Context) error {
	if err := r.db.PingContext(ctx); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "ping"),
		)
	}
	return nil
}

// Close closes the repository connection and releases resources.
// After calling Close, no other methods should be called.
func (r *PostgresRepository) Close() error {
	if err := r.db.Close(); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "close"),
		)
	}
	return nil
}
