package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/itsatony/go-hookd"
)

// =============================================================================
// DATABASE TEST HELPERS
// =============================================================================

// TestDB provides database utilities for integration tests
type TestDB struct {
	t       *testing.T
	repo    *hookd.PostgresRepository
	connStr string
	db      *sql.DB
}

// NewTestDB creates a new test database instance
// It checks for DATABASE_URL environment variable or uses default
func NewTestDB(t *testing.T) *TestDB {
	connStr := os.Getenv("HOOKD_TEST_DB")
	if connStr == "" {
		connStr = "postgres://hookd:hookd@localhost:54321/hookd?sslmode=disable"
	}

	// Check if database is available
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Skip(fmt.Sprintf("PostgreSQL not available: %v\n%s", err, databaseSetupMessage()))
	}

	if err := db.Ping(); err != nil {
		t.Skip(fmt.Sprintf("PostgreSQL not reachable: %v\n%s", err, databaseSetupMessage()))
	}

	repo, err := hookd.NewPostgresRepository(connStr)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	return &TestDB{
		t:       t,
		repo:    repo,
		connStr: connStr,
		db:      db,
	}
}

// Repository returns the repository instance
func (tdb *TestDB) Repository() hookd.Repository {
	return tdb.repo
}

// DB returns the underlying sql.DB
func (tdb *TestDB) DB() *sql.DB {
	return tdb.db
}

// Truncate removes all data from test tables (useful for cleanup)
func (tdb *TestDB) Truncate() {
	ctx := context.Background()

	tables := []string{
		hookd.TableDeliveryAttempts,
		hookd.TableDeliveries,
		hookd.TableIdempotencyStore,
		hookd.TableCircuitBreakerState,
		hookd.TableSubscriptions,
	}

	for _, table := range tables {
		_, err := tdb.db.ExecContext(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
		if err != nil {
			tdb.t.Fatalf("Failed to truncate table %s: %v", table, err)
		}
	}
}

// Close closes the database connection
func (tdb *TestDB) Close() {
	if tdb.repo != nil {
		tdb.repo.Close()
	}
	if tdb.db != nil {
		tdb.db.Close()
	}
}

// =============================================================================
// TRANSACTION-BASED TEST ISOLATION
// =============================================================================

// TxTestFunc is a test function that receives a transactional repository
type TxTestFunc func(t *testing.T, repo hookd.Repository)

// WithTransaction runs a test function within a transaction that auto-rolls back
// This provides test isolation and enables parallel test execution
func WithTransaction(t *testing.T, testFunc TxTestFunc) {
	tdb := NewTestDB(t)
	defer tdb.Close()

	ctx := context.Background()

	// Begin transaction
	tx, err := tdb.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Create transactional repository
	config := hookd.NewConfig(tdb.connStr)
	txRepo := &TransactionalRepository{
		tx:     tx,
		config: config,
	}

	// Always rollback transaction (even on panic)
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r) // Re-panic after rollback
		}
		tx.Rollback()
	}()

	// Run test function
	testFunc(t, txRepo)
}

// =============================================================================
// TRANSACTIONAL REPOSITORY WRAPPER
// =============================================================================

// TransactionalRepository wraps a transaction to implement Repository interface
// This allows tests to run in isolated transactions
type TransactionalRepository struct {
	tx     *sql.Tx
	config *hookd.Config
}

// Note: TransactionalRepository implements most Repository methods
// Some methods like BeginTx are not applicable within a transaction

// CreateSubscription creates a subscription within the transaction
func (r *TransactionalRepository) CreateSubscription(ctx context.Context, sub *hookd.Subscription) error {
	query := `
		INSERT INTO subscriptions (
			id, tenant_id, url, event_types, secret, status,
			headers, metadata, retry_policy, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	_, err := r.tx.ExecContext(ctx, query,
		sub.ID, sub.TenantID, sub.URL, sub.EventTypes, sub.Secret,
		sub.Status, sub.Headers, sub.Metadata, sub.RetryPolicy,
		sub.CreatedAt, sub.UpdatedAt,
	)

	return err
}

// GetSubscription retrieves a subscription by ID within the transaction
func (r *TransactionalRepository) GetSubscription(ctx context.Context, id string) (*hookd.Subscription, error) {
	query := `
		SELECT id, tenant_id, url, event_types, secret, status,
			   headers, metadata, retry_policy, created_at, updated_at
		FROM subscriptions
		WHERE id = $1
	`

	var sub hookd.Subscription
	err := r.tx.QueryRowContext(ctx, query, id).Scan(
		&sub.ID, &sub.TenantID, &sub.URL, &sub.EventTypes, &sub.Secret,
		&sub.Status, &sub.Headers, &sub.Metadata, &sub.RetryPolicy,
		&sub.CreatedAt, &sub.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, hookd.NewSubscriptionNotFoundError(id)
	}

	return &sub, err
}

// UpdateSubscription updates a subscription within the transaction
func (r *TransactionalRepository) UpdateSubscription(ctx context.Context, sub *hookd.Subscription) error {
	query := `
		UPDATE subscriptions
		SET url = $2, event_types = $3, secret = $4, status = $5,
			headers = $6, metadata = $7, retry_policy = $8, updated_at = $9
		WHERE id = $1
	`

	result, err := r.tx.ExecContext(ctx, query,
		sub.ID, sub.URL, sub.EventTypes, sub.Secret, sub.Status,
		sub.Headers, sub.Metadata, sub.RetryPolicy, time.Now(),
	)

	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return hookd.NewSubscriptionNotFoundError(sub.ID)
	}

	return nil
}

// DeleteSubscription deletes a subscription within the transaction
func (r *TransactionalRepository) DeleteSubscription(ctx context.Context, id string) error {
	query := `DELETE FROM subscriptions WHERE id = $1`

	result, err := r.tx.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return hookd.NewSubscriptionNotFoundError(id)
	}

	return nil
}

// ListSubscriptions lists subscriptions with filters within the transaction
func (r *TransactionalRepository) ListSubscriptions(ctx context.Context, filter *hookd.SubscriptionFilter) ([]*hookd.Subscription, error) {
	// Simplified implementation for testing
	query := `
		SELECT id, tenant_id, url, event_types, secret, status,
			   headers, metadata, retry_policy, created_at, updated_at
		FROM subscriptions
		WHERE tenant_id = $1
	`

	rows, err := r.tx.QueryContext(ctx, query, filter.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []*hookd.Subscription
	for rows.Next() {
		var sub hookd.Subscription
		err := rows.Scan(
			&sub.ID, &sub.TenantID, &sub.URL, &sub.EventTypes, &sub.Secret,
			&sub.Status, &sub.Headers, &sub.Metadata, &sub.RetryPolicy,
			&sub.CreatedAt, &sub.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		subs = append(subs, &sub)
	}

	return subs, rows.Err()
}

// CreateDelivery creates a delivery within the transaction
func (r *TransactionalRepository) CreateDelivery(ctx context.Context, delivery *hookd.Delivery) error {
	query := `
		INSERT INTO deliveries (
			id, subscription_id, tenant_id, event_type, payload, status,
			attempt_count, max_attempts, next_retry_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := r.tx.ExecContext(ctx, query,
		delivery.ID, delivery.SubscriptionID, delivery.TenantID,
		delivery.EventType, delivery.Payload, delivery.Status,
		delivery.AttemptCount, delivery.MaxAttempts,
		delivery.NextRetryAt, delivery.CreatedAt,
	)

	return err
}

// GetDelivery retrieves a delivery by ID within the transaction
func (r *TransactionalRepository) GetDelivery(ctx context.Context, id string) (*hookd.Delivery, error) {
	query := `
		SELECT id, subscription_id, tenant_id, event_type, payload, status,
			   attempt_count, max_attempts, next_retry_at,
			   completed_at, created_at
		FROM deliveries
		WHERE id = $1
	`

	var delivery hookd.Delivery
	err := r.tx.QueryRowContext(ctx, query, id).Scan(
		&delivery.ID, &delivery.SubscriptionID, &delivery.TenantID,
		&delivery.EventType, &delivery.Payload, &delivery.Status,
		&delivery.AttemptCount, &delivery.MaxAttempts,
		&delivery.NextRetryAt, &delivery.CompletedAt,
		&delivery.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, hookd.NewDeliveryNotFoundError(id)
	}

	return &delivery, err
}

// UpdateDelivery updates a delivery within the transaction
func (r *TransactionalRepository) UpdateDelivery(ctx context.Context, delivery *hookd.Delivery) error {
	query := `
		UPDATE deliveries
		SET status = $2, attempt_count = $3, next_retry_at = $4,
			completed_at = $5
		WHERE id = $1
	`

	result, err := r.tx.ExecContext(ctx, query,
		delivery.ID, delivery.Status, delivery.AttemptCount,
		delivery.NextRetryAt, delivery.CompletedAt,
	)

	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return hookd.NewDeliveryNotFoundError(delivery.ID)
	}

	return nil
}

// CreateDeliveryAttempt creates a delivery attempt within the transaction
func (r *TransactionalRepository) CreateDeliveryAttempt(ctx context.Context, attempt *hookd.DeliveryAttempt) error {
	query := `
		INSERT INTO delivery_attempts (
			id, delivery_id, attempt_number, status_code, error,
			response_body, duration_ms, attempted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.tx.ExecContext(ctx, query,
		attempt.ID, attempt.DeliveryID, attempt.AttemptNumber,
		attempt.StatusCode, attempt.Error, attempt.ResponseBody,
		attempt.DurationMs, attempt.AttemptedAt,
	)

	return err
}

// GetDeliveryAttempts retrieves all attempts for a delivery within the transaction
func (r *TransactionalRepository) GetDeliveryAttempts(ctx context.Context, deliveryID string) ([]*hookd.DeliveryAttempt, error) {
	query := `
		SELECT id, delivery_id, attempt_number, status_code, error,
			   response_body, duration_ms, attempted_at
		FROM delivery_attempts
		WHERE delivery_id = $1
		ORDER BY attempt_number ASC
	`

	rows, err := r.tx.QueryContext(ctx, query, deliveryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []*hookd.DeliveryAttempt
	for rows.Next() {
		var attempt hookd.DeliveryAttempt
		err := rows.Scan(
			&attempt.ID, &attempt.DeliveryID, &attempt.AttemptNumber,
			&attempt.StatusCode, &attempt.Error, &attempt.ResponseBody,
			&attempt.DurationMs, &attempt.AttemptedAt,
		)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, &attempt)
	}

	return attempts, rows.Err()
}

// GetPendingDeliveries retrieves pending deliveries within the transaction
func (r *TransactionalRepository) GetPendingDeliveries(ctx context.Context, limit int) ([]*hookd.Delivery, error) {
	query := `
		SELECT id, subscription_id, tenant_id, event_type, payload, status,
			   attempt_count, max_attempts, next_retry_at,
			   completed_at, created_at
		FROM deliveries
		WHERE status = $1
			AND (next_retry_at IS NULL OR next_retry_at <= $2)
		ORDER BY created_at ASC
		LIMIT $3
		FOR UPDATE SKIP LOCKED
	`

	rows, err := r.tx.QueryContext(ctx, query, hookd.DeliveryStatusPending, time.Now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []*hookd.Delivery
	for rows.Next() {
		var delivery hookd.Delivery
		err := rows.Scan(
			&delivery.ID, &delivery.SubscriptionID, &delivery.TenantID,
			&delivery.EventType, &delivery.Payload, &delivery.Status,
			&delivery.AttemptCount, &delivery.MaxAttempts,
			&delivery.NextRetryAt, &delivery.CompletedAt,
			&delivery.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, &delivery)
	}

	return deliveries, rows.Err()
}

// ListDeliveries retrieves deliveries matching a filter (stub implementation for testutil)
func (r *TransactionalRepository) ListDeliveries(ctx context.Context, filter *hookd.DeliveryFilter) ([]*hookd.Delivery, error) {
	// This is a simplified test implementation
	// For production testing, use the actual PostgresRepository
	return nil, fmt.Errorf("ListDeliveries not implemented in testutil TransactionalRepository")
}

// GetSubscriptionByTenantAndURL retrieves a subscription by tenant and URL
func (r *TransactionalRepository) GetSubscriptionByTenantAndURL(ctx context.Context, tenantID, url string) (*hookd.Subscription, error) {
	query := `
		SELECT id, tenant_id, url, event_types, secret, status,
			   headers, metadata, retry_policy, created_at, updated_at
		FROM subscriptions
		WHERE tenant_id = $1 AND url = $2
	`

	var sub hookd.Subscription
	err := r.tx.QueryRowContext(ctx, query, tenantID, url).Scan(
		&sub.ID, &sub.TenantID, &sub.URL, &sub.EventTypes, &sub.Secret,
		&sub.Status, &sub.Headers, &sub.Metadata, &sub.RetryPolicy,
		&sub.CreatedAt, &sub.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, hookd.NewSubscriptionNotFoundError(tenantID + "/" + url)
	}

	return &sub, err
}

// BeginTx is not applicable for TransactionalRepository (already in transaction)
func (r *TransactionalRepository) BeginTx(ctx context.Context) (hookd.RepositoryTx, error) {
	return nil, fmt.Errorf("BeginTx not supported on TransactionalRepository")
}

// Ping is not applicable for TransactionalRepository (no direct connection)
func (r *TransactionalRepository) Ping(ctx context.Context) error {
	return nil
}

// Stub implementations for other Repository methods
func (r *TransactionalRepository) MoveToDeadLetter(ctx context.Context, deliveryID, reason string) error {
	return nil
}

func (r *TransactionalRepository) CheckIdempotency(ctx context.Context, key, subscriptionID string) (bool, error) {
	return false, nil
}

func (r *TransactionalRepository) StoreIdempotencyKey(ctx context.Context, key, subscriptionID string, expiresAt time.Time) error {
	return nil
}

func (r *TransactionalRepository) GetCircuitBreakerState(ctx context.Context, endpoint string) (*hookd.CircuitBreakerState, error) {
	return nil, nil
}

func (r *TransactionalRepository) UpdateCircuitBreakerState(ctx context.Context, state *hookd.CircuitBreakerState) error {
	return nil
}

func (r *TransactionalRepository) Close() error {
	// Transaction will be rolled back by caller
	return nil
}

// =============================================================================
// UTILITY FUNCTIONS
// =============================================================================

// databaseSetupMessage returns helpful setup instructions when DB is unavailable
func databaseSetupMessage() string {
	return `
╔════════════════════════════════════════════════════════════╗
║  PostgreSQL Integration Tests                              ║
╠════════════════════════════════════════════════════════════╣
║  Quick Start:                                              ║
║    1. Start database:   docker-compose up -d              ║
║    2. Run migrations:   ./scripts/db-dev.sh migrate       ║
║    3. Re-run tests:     go test ./...                     ║
║                                                            ║
║  Or set HOOKD_TEST_DB to use your own database:           ║
║    export HOOKD_TEST_DB="postgres://user:pass@host/db"   ║
╚════════════════════════════════════════════════════════════╝
`
}

// WaitForCondition polls a condition function until it returns true or times out
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, checkInterval time.Duration) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(checkInterval)
	}

	t.Fatalf("Condition not met within %v", timeout)
}
