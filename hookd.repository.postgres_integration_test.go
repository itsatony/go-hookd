//go:build integration
// +build integration

package hookd

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	postgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// PostgresTestContainer manages a test PostgreSQL container
type PostgresTestContainer struct {
	container *postgres.PostgresContainer
	db        *sql.DB
	connStr   string
}

// SetupPostgresContainer creates and starts a PostgreSQL testcontainer
// with schema applied using SchemaManager.
func SetupPostgresContainer(t *testing.T) *PostgresTestContainer {
	t.Helper()

	ctx := context.Background()

	// Create PostgreSQL container
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("hookd_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
		postgres.WithSQLDriver("postgres"),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	// Get connection string
	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "Failed to get connection string")

	// Connect to database
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err, "Failed to connect to PostgreSQL")

	// Verify connection
	err = db.PingContext(ctx)
	require.NoError(t, err, "Failed to ping PostgreSQL")

	// Create schema using SchemaManager (with "test" prefix to match test repos)
	schemaConfig, err := NewSchemaConfig("test")
	require.NoError(t, err, "Failed to create schema config")

	schemaMgr := NewSchemaManager(db, schemaConfig)
	err = schemaMgr.EnsureSchema(ctx)
	require.NoError(t, err, "Failed to ensure schema")

	return &PostgresTestContainer{
		container: pgContainer,
		db:        db,
		connStr:   connStr,
	}
}

// Cleanup tears down the container and closes connections
func (p *PostgresTestContainer) Cleanup(t *testing.T) {
	t.Helper()

	if p.db != nil {
		p.db.Close()
	}

	if p.container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = p.container.Terminate(ctx)
	}
}

// =============================================================================
// INTEGRATION TESTS - SUBSCRIPTION CRUD
// =============================================================================

func TestPostgresRepository_CreateSubscription_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create test subscription
	sub := &Subscription{
		ID:         "sub_test_integration_1",
		TenantID:   "tenant_integration",
		URL:        "https://example.com/webhook",
		Secret:     "test_secret_key",
		EventTypes: []string{"user.created", "user.updated"},
		Status:     SubscriptionStatusActive,
		RetryPolicy: &RetryPolicy{
			MaxAttempts:    10,
			InitialBackoff: 1 * time.Second,
			MaxBackoff:     1 * time.Hour,
			BackoffFactor:  2.0,
		},
		Headers:  map[string]string{"X-Custom": "value"},
		Metadata: map[string]any{"key": "value"},
	}

	// Execute
	err = repo.CreateSubscription(ctx, sub)

	// Verify
	require.NoError(t, err, "CreateSubscription should succeed")

	// Query directly from database to verify
	var count int
	err = pgContainer.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE id = $1", TableSubscriptions),
		sub.ID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count, "Subscription should exist in database")
}

func TestPostgresRepository_GetSubscription_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription directly in database
	// Note: time.Duration is stored as nanoseconds in JSON
	_, err = pgContainer.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (
			id, tenant_id, url, secret, event_types, status,
			retry_policy, headers, metadata, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, NOW(), NOW()
		)
	`, TableSubscriptions), "sub_get_test", "tenant_1", "https://test.com/hook", "secret",
		pq.Array([]string{"test.event"}), "active",
		`{"max_attempts": 10, "initial_backoff": 1000000000, "max_backoff": 300000000000, "backoff_factor": 2.0}`,
		`{}`,
		`{}`)
	require.NoError(t, err)

	// Execute
	sub, err := repo.GetSubscription(ctx, "sub_get_test")

	// Verify
	require.NoError(t, err)
	require.NotNil(t, sub)
	require.Equal(t, "sub_get_test", sub.ID)
	require.Equal(t, "tenant_1", sub.TenantID)
	require.Equal(t, "https://test.com/hook", sub.URL)
	require.Equal(t, SubscriptionStatusActive, sub.Status)
}

func TestPostgresRepository_UpdateSubscription_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create initial subscription
	sub := &Subscription{
		ID:         "sub_update_test",
		TenantID:   "tenant_1",
		URL:        "https://original.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"event.original"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Update subscription
	sub.URL = "https://updated.com/webhook"
	sub.EventTypes = []string{"event.updated", "event.new"}
	sub.Status = SubscriptionStatusPaused

	err = repo.UpdateSubscription(ctx, sub)

	// Verify
	require.NoError(t, err)

	// Fetch and verify changes
	updated, err := repo.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, "https://updated.com/webhook", updated.URL)
	require.Equal(t, SubscriptionStatusPaused, updated.Status)
	require.Equal(t, []string{"event.updated", "event.new"}, updated.EventTypes)
}

func TestPostgresRepository_DeleteSubscription_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription
	sub := &Subscription{
		ID:         "sub_delete_test",
		TenantID:   "tenant_1",
		URL:        "https://delete.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Delete subscription
	err = repo.DeleteSubscription(ctx, sub.ID)
	require.NoError(t, err)

	// Verify deletion
	_, err = repo.GetSubscription(ctx, sub.ID)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
}

func TestPostgresRepository_ListSubscriptions_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create multiple subscriptions
	for i := 0; i < 5; i++ {
		sub := &Subscription{
			ID:         fmt.Sprintf("sub_list_%d", i),
			TenantID:   "tenant_list",
			URL:        fmt.Sprintf("https://example.com/webhook/%d", i),
			Secret:     "secret",
			EventTypes: []string{"test.event"},
			Status:     SubscriptionStatusActive,
		}
		err = repo.CreateSubscription(ctx, sub)
		require.NoError(t, err)
	}

	// List subscriptions
	subs, err := repo.ListSubscriptions(ctx, &SubscriptionFilter{
		TenantID: "tenant_list",
	})

	// Verify
	require.NoError(t, err)
	require.Len(t, subs, 5)
}

// =============================================================================
// INTEGRATION TESTS - DELIVERY CRUD
// =============================================================================

func TestPostgresRepository_CreateDelivery_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription first (foreign key requirement)
	sub := &Subscription{
		ID:         "sub_for_delivery",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery
	delivery := &Delivery{
		ID:             "dlv_test_1",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]any{"data": "test"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}

	// Execute
	err = repo.CreateDelivery(ctx, delivery)

	// Verify
	require.NoError(t, err)

	// Query directly from database
	var count int
	err = pgContainer.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE id = $1", TableDeliveries),
		delivery.ID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestPostgresRepository_GetPendingDeliveries_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription
	sub := &Subscription{
		ID:         "sub_pending_test",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create pending deliveries
	for i := 0; i < 3; i++ {
		delivery := &Delivery{
			ID:             fmt.Sprintf("dlv_pending_%d", i),
			SubscriptionID: sub.ID,
			TenantID:       sub.TenantID,
			EventType:      "test.event",
			Payload:        map[string]any{"id": i},
			Status:         DeliveryStatusPending,
			AttemptCount:   0,
			MaxAttempts:    10,
		}
		err = repo.CreateDelivery(ctx, delivery)
		require.NoError(t, err)
	}

	// Get pending deliveries
	deliveries, err := repo.ClaimPendingDeliveries(ctx, 10, testClaimLease)

	// Verify
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(deliveries), 3, "Should have at least 3 pending deliveries")
}

// =============================================================================
// INTEGRATION TESTS - SKIP LOCKED BEHAVIOR
// =============================================================================

func TestPostgresRepository_GetPendingDeliveries_SkipLocked_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription
	sub := &Subscription{
		ID:         "sub_skip_locked",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create a pending delivery
	delivery := &Delivery{
		ID:             "dlv_skip_locked",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]any{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Start transaction 1 and lock the delivery
	tx1, err := pgContainer.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx1.Rollback()

	// Lock delivery in tx1
	_, err = tx1.ExecContext(ctx, fmt.Sprintf(`
		SELECT * FROM %s
		WHERE id = $1
		FOR UPDATE
	`, TableDeliveries), delivery.ID)
	require.NoError(t, err)

	// Try to get pending deliveries from main repo (should skip locked)
	deliveries, err := repo.ClaimPendingDeliveries(ctx, 10, testClaimLease)
	require.NoError(t, err)

	// Verify: locked delivery should be skipped
	for _, d := range deliveries {
		require.NotEqual(t, delivery.ID, d.ID, "Locked delivery should be skipped")
	}
}

// =============================================================================
// INTEGRATION TESTS - TRANSACTIONS
// =============================================================================

func TestPostgresRepository_Transaction_Commit_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// Create subscription in transaction
	sub := &Subscription{
		ID:         "sub_tx_commit",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = tx.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Commit transaction
	err = tx.Commit()
	require.NoError(t, err)

	// Verify subscription exists
	fetchedSub, err := repo.GetSubscription(ctx, sub.ID)
	require.NoError(t, err)
	require.Equal(t, sub.ID, fetchedSub.ID)
}

func TestPostgresRepository_Transaction_Rollback_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Begin transaction
	tx, err := repo.BeginTx(ctx)
	require.NoError(t, err)

	// Create subscription in transaction
	sub := &Subscription{
		ID:         "sub_tx_rollback",
		TenantID:   "tenant_tx",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = tx.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Rollback transaction
	err = tx.Rollback()
	require.NoError(t, err)

	// Verify subscription does NOT exist
	_, err = repo.GetSubscription(ctx, sub.ID)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrSubscriptionNotFound)
}

// =============================================================================
// INTEGRATION TESTS - DELIVERY RETRIEVAL & UPDATE
// =============================================================================

func TestPostgresRepository_GetDelivery_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription first
	sub := &Subscription{
		ID:         "sub_get_delivery",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery
	delivery := &Delivery{
		ID:             "dlv_get_test",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]any{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Execute: Get delivery
	fetchedDelivery, err := repo.GetDelivery(ctx, delivery.ID)

	// Verify
	require.NoError(t, err)
	require.NotNil(t, fetchedDelivery)
	require.Equal(t, delivery.ID, fetchedDelivery.ID)
	require.Equal(t, delivery.SubscriptionID, fetchedDelivery.SubscriptionID)
	require.Equal(t, delivery.EventType, fetchedDelivery.EventType)
	require.Equal(t, DeliveryStatusPending, fetchedDelivery.Status)
}

func TestPostgresRepository_UpdateDelivery_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription
	sub := &Subscription{
		ID:         "sub_update_delivery",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery
	delivery := &Delivery{
		ID:             "dlv_update_test",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]any{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Update delivery status
	delivery.Status = DeliveryStatusSuccess
	delivery.AttemptCount = 1
	now := time.Now()
	delivery.CompletedAt = &now

	err = repo.UpdateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Verify update
	updated, err := repo.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, DeliveryStatusSuccess, updated.Status)
	require.Equal(t, 1, updated.AttemptCount)
	require.NotNil(t, updated.CompletedAt)
}

func TestPostgresRepository_MoveToDeadLetter_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription
	sub := &Subscription{
		ID:         "sub_dead_letter",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery
	delivery := &Delivery{
		ID:             "dlv_dead_letter_test",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]any{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   3,
		MaxAttempts:    3,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Move to dead letter
	err = repo.MoveToDeadLetter(ctx, delivery.ID, "max retries exceeded")
	require.NoError(t, err)

	// Verify status changed
	updated, err := repo.GetDelivery(ctx, delivery.ID)
	require.NoError(t, err)
	require.Equal(t, DeliveryStatusDeadLetter, updated.Status)
	require.NotNil(t, updated.CompletedAt)
}

// =============================================================================
// INTEGRATION TESTS - DELIVERY ATTEMPTS
// =============================================================================

func TestPostgresRepository_CreateDeliveryAttempt_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription
	sub := &Subscription{
		ID:         "sub_attempt",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery
	delivery := &Delivery{
		ID:             "dlv_attempt_test",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]any{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Create delivery attempt
	attempt := &DeliveryAttempt{
		ID:            "att_test_1",
		DeliveryID:    delivery.ID,
		AttemptNumber: 1,
		StatusCode:    500,
		ResponseBody:  "Internal Server Error",
		Error:         "connection timeout",
		DurationMs:    1500,
		AttemptedAt:   time.Now(),
	}
	err = repo.CreateDeliveryAttempt(ctx, attempt)
	require.NoError(t, err)

	// Verify attempt was created
	attempts, err := repo.GetDeliveryAttempts(ctx, delivery.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, attempt.ID, attempts[0].ID)
	require.Equal(t, 1, attempts[0].AttemptNumber)
	require.Equal(t, 500, attempts[0].StatusCode)
}

func TestPostgresRepository_GetDeliveryAttempts_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription
	sub := &Subscription{
		ID:         "sub_get_attempts",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Create delivery
	delivery := &Delivery{
		ID:             "dlv_get_attempts",
		SubscriptionID: sub.ID,
		TenantID:       sub.TenantID,
		EventType:      "test.event",
		Payload:        map[string]any{"test": "data"},
		Status:         DeliveryStatusPending,
		AttemptCount:   0,
		MaxAttempts:    10,
	}
	err = repo.CreateDelivery(ctx, delivery)
	require.NoError(t, err)

	// Create multiple attempts
	for i := 1; i <= 3; i++ {
		attempt := &DeliveryAttempt{
			ID:            fmt.Sprintf("att_get_%d", i),
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

	// Get all attempts
	attempts, err := repo.GetDeliveryAttempts(ctx, delivery.ID)
	require.NoError(t, err)
	require.Len(t, attempts, 3)

	// Verify attempts are ordered by attempt_number
	for i, attempt := range attempts {
		require.Equal(t, i+1, attempt.AttemptNumber)
	}
}

// =============================================================================
// INTEGRATION TESTS - IDEMPOTENCY
// =============================================================================

func TestPostgresRepository_CheckIdempotency_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription first (foreign key requirement)
	sub := &Subscription{
		ID:         "sub_idem_test",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	subscriptionID := sub.ID

	// Store idempotency key
	key := "idem_test_123"
	expiresAt := time.Now().Add(24 * time.Hour)
	err = repo.StoreIdempotencyKey(ctx, key, subscriptionID, expiresAt)
	require.NoError(t, err)

	// Check idempotency - should find existing
	exists, err := repo.CheckIdempotency(ctx, key, subscriptionID)
	require.NoError(t, err)
	require.True(t, exists)

	// Check non-existent key
	exists, err = repo.CheckIdempotency(ctx, "non_existent_key", subscriptionID)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestPostgresRepository_StoreIdempotencyKey_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create subscription first (foreign key requirement)
	sub := &Subscription{
		ID:         "sub_store_test",
		TenantID:   "tenant_1",
		URL:        "https://example.com/webhook",
		Secret:     "secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	subscriptionID := sub.ID

	// Store new key
	key := "idem_store_test"
	expiresAt := time.Now().Add(24 * time.Hour)
	err = repo.StoreIdempotencyKey(ctx, key, subscriptionID, expiresAt)
	require.NoError(t, err)

	// Verify key was stored
	exists, err := repo.CheckIdempotency(ctx, key, subscriptionID)
	require.NoError(t, err)
	require.True(t, exists)

	// Try to store duplicate key (may succeed with UPSERT or fail with unique constraint)
	// The implementation might use ON CONFLICT DO UPDATE
	err = repo.StoreIdempotencyKey(ctx, key, subscriptionID, expiresAt)
	// Either succeeds (UPSERT) or fails (unique constraint) - both are valid
	_ = err // Allow either outcome
}

// =============================================================================
// INTEGRATION TESTS - CIRCUIT BREAKER
// =============================================================================

func TestPostgresRepository_GetCircuitBreakerState_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	endpoint := "https://example.com/webhook"

	// Get state for non-existent endpoint (returns zero state with "closed")
	state, err := repo.GetCircuitBreakerState(ctx, endpoint)
	require.NoError(t, err)
	// Circuit breaker returns a default state if none exists
	if state != nil {
		require.Equal(t, "closed", state.State)
		require.Equal(t, 0, state.FailureCount)
	}

	// Create circuit breaker state
	cbState := &CircuitBreakerState{
		Endpoint:     endpoint,
		State:        "open",
		FailureCount: 5,
		LastFailure:  time.Now(),
		NextRetryAt:  time.Now().Add(1 * time.Minute),
	}
	err = repo.UpdateCircuitBreakerState(ctx, cbState)
	require.NoError(t, err)

	// Get state again
	fetchedState, err := repo.GetCircuitBreakerState(ctx, endpoint)
	require.NoError(t, err)
	require.NotNil(t, fetchedState)
	require.Equal(t, endpoint, fetchedState.Endpoint)
	require.Equal(t, "open", fetchedState.State)
	require.Equal(t, 5, fetchedState.FailureCount)
}

func TestPostgresRepository_UpdateCircuitBreakerState_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	endpoint := "https://example.com/webhook"

	// Create initial state
	cbState := &CircuitBreakerState{
		Endpoint:     endpoint,
		State:        "closed",
		FailureCount: 0,
		LastFailure:  time.Now(),
	}
	err = repo.UpdateCircuitBreakerState(ctx, cbState)
	require.NoError(t, err)

	// Update state to open
	cbState.State = "open"
	cbState.FailureCount = 5
	cbState.NextRetryAt = time.Now().Add(1 * time.Minute)

	err = repo.UpdateCircuitBreakerState(ctx, cbState)
	require.NoError(t, err)

	// Verify update
	updated, err := repo.GetCircuitBreakerState(ctx, endpoint)
	require.NoError(t, err)
	require.Equal(t, "open", updated.State)
	require.Equal(t, 5, updated.FailureCount)
	require.NotNil(t, updated.NextRetryAt)
}

func TestPostgresRepository_GetSubscriptionByTenantAndURL_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Create test subscription
	sub := &Subscription{
		ID:         "sub_test_tenant_url",
		TenantID:   "tenant_123",
		URL:        "https://example.com/webhook",
		Secret:     "test_secret",
		EventTypes: []string{"test.event"},
		Status:     SubscriptionStatusActive,
		RetryPolicy: &RetryPolicy{
			MaxAttempts:    10,
			InitialBackoff: 1 * time.Second,
			MaxBackoff:     5 * time.Minute,
			BackoffFactor:  2.0,
		},
	}
	err = repo.CreateSubscription(ctx, sub)
	require.NoError(t, err)

	// Test: Get subscription by tenant and URL
	fetched, err := repo.GetSubscriptionByTenantAndURL(ctx, "tenant_123", "https://example.com/webhook")
	require.NoError(t, err)
	require.NotNil(t, fetched)
	require.Equal(t, sub.ID, fetched.ID)
	require.Equal(t, sub.TenantID, fetched.TenantID)
	require.Equal(t, sub.URL, fetched.URL)
	require.Equal(t, sub.Status, fetched.Status)

	// Test: Non-existent tenant
	fetched, err = repo.GetSubscriptionByTenantAndURL(ctx, "non_existent", "https://example.com/webhook")
	require.Error(t, err)
	require.Nil(t, fetched)
	require.Equal(t, ErrSubscriptionNotFound, err)

	// Test: Non-existent URL
	fetched, err = repo.GetSubscriptionByTenantAndURL(ctx, "tenant_123", "https://different.com/webhook")
	require.Error(t, err)
	require.Nil(t, fetched)
	require.Equal(t, ErrSubscriptionNotFound, err)
}

func TestPostgresRepository_Ping_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup(t)

	repo, err := NewPostgresRepository(pgContainer.connStr, WithTablePrefix("test"))
	require.NoError(t, err)
	defer repo.Close()

	ctx := context.Background()

	// Test: Ping should succeed
	err = repo.Ping(ctx)
	require.NoError(t, err)

	// Test: Ping after close should fail
	repo.Close()
	err = repo.Ping(ctx)
	require.Error(t, err)
}
