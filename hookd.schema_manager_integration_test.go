//go:build integration
// +build integration

// Package hookd provides webhook management functionality.
//
// This file contains integration tests for the SchemaManager.
// These tests require a PostgreSQL database and are run with: go test -tags=integration
//
// Excellence. Always.
package hookd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// INTEGRATION TESTS (require PostgreSQL)
// =============================================================================

// TestIntegration_SchemaManager_EnsureSchema tests schema creation with real database.
func TestIntegration_SchemaManager_EnsureSchema(t *testing.T) {
	// Setup test container
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a schema config with a NEW prefix (not used by SetupPostgresContainer)
	schemaConfig, err := NewSchemaConfig("ensuretest")
	require.NoError(t, err)

	// Create schema manager with the existing connection
	manager := NewSchemaManager(pgContainer.db, schemaConfig)

	t.Run("creates_schema_when_not_exists", func(t *testing.T) {
		// First verify schema doesn't exist
		exists, err := manager.SchemaExists(ctx)
		require.NoError(t, err)
		assert.False(t, exists, "schema should not exist initially")

		// Create schema
		err = manager.EnsureSchema(ctx)
		require.NoError(t, err)

		// Verify schema now exists
		exists, err = manager.SchemaExists(ctx)
		require.NoError(t, err)
		assert.True(t, exists, "schema should exist after EnsureSchema")

		// Verify version
		version, err := manager.GetSchemaVersion(ctx)
		require.NoError(t, err)
		assert.Equal(t, SchemaVersion, version)
	})

	t.Run("idempotent_when_called_multiple_times", func(t *testing.T) {
		// Call EnsureSchema again
		err := manager.EnsureSchema(ctx)
		require.NoError(t, err)

		// Schema should still exist
		exists, err := manager.SchemaExists(ctx)
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("get_schema_info_returns_complete_info", func(t *testing.T) {
		info, err := manager.GetSchemaInfo(ctx)
		require.NoError(t, err)

		assert.True(t, info.Exists)
		assert.Equal(t, SchemaVersion, info.Version)
		assert.Equal(t, "ensuretest", info.Prefix)
		assert.Len(t, info.TableNames, 5)
		assert.Equal(t, SchemaVersion, info.LatestVersion)
		assert.False(t, info.NeedsUpgrade)
	})

	t.Run("verifies_tables_created", func(t *testing.T) {
		// Query to check if tables exist
		tables := []string{
			"ensuretest_hookd_subscriptions",
			"ensuretest_hookd_deliveries",
			"ensuretest_hookd_delivery_attempts",
			"ensuretest_hookd_idempotency_store",
			"ensuretest_hookd_circuit_breaker_state",
		}

		for _, tableName := range tables {
			var exists bool
			err := pgContainer.db.QueryRowContext(ctx, `
				SELECT EXISTS (
					SELECT FROM information_schema.tables
					WHERE table_schema = 'public'
					AND table_name = $1
				)
			`, tableName).Scan(&exists)
			require.NoError(t, err)
			assert.True(t, exists, "table %s should exist", tableName)
		}
	})

	// Cleanup
	t.Run("drop_schema_removes_all_objects", func(t *testing.T) {
		err := manager.DropSchema(ctx)
		require.NoError(t, err)

		exists, err := manager.SchemaExists(ctx)
		require.NoError(t, err)
		assert.False(t, exists, "schema should not exist after DropSchema")
	})
}

// TestIntegration_SchemaManager_FromURL tests creating manager from URL.
func TestIntegration_SchemaManager_FromURL(t *testing.T) {
	// Setup test container
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup()

	t.Run("creates_manager_from_valid_url", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("urltest")
		require.NoError(t, err)

		manager, err := NewSchemaManagerFromURL(pgContainer.connStr, schemaConfig)
		require.NoError(t, err)
		require.NotNil(t, manager)

		assert.NotNil(t, manager.DB())
		assert.Equal(t, schemaConfig, manager.SchemaConfig())

		// Cleanup
		manager.Close()
	})

	t.Run("returns_error_for_nil_schema_config", func(t *testing.T) {
		_, err := NewSchemaManagerFromURL(pgContainer.connStr, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "schema configuration is required")
	})

	t.Run("returns_error_for_invalid_url", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("invalid")
		require.NoError(t, err)

		_, err = NewSchemaManagerFromURL("postgres://invalid:invalid@localhost:99999/invalid", schemaConfig)
		assert.Error(t, err)
	})
}

// TestIntegration_SchemaManager_MultipleSchemas tests multiple schemas in same database.
func TestIntegration_SchemaManager_MultipleSchemas(t *testing.T) {
	// Setup test container
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two different schema configs
	schemaConfigA, err := NewSchemaConfig("servicea")
	require.NoError(t, err)
	schemaConfigB, err := NewSchemaConfig("serviceb")
	require.NoError(t, err)

	managerA := NewSchemaManager(pgContainer.db, schemaConfigA)
	managerB := NewSchemaManager(pgContainer.db, schemaConfigB)

	t.Run("creates_independent_schemas", func(t *testing.T) {
		// Create both schemas
		err := managerA.EnsureSchema(ctx)
		require.NoError(t, err)

		err = managerB.EnsureSchema(ctx)
		require.NoError(t, err)

		// Both should exist
		existsA, err := managerA.SchemaExists(ctx)
		require.NoError(t, err)
		assert.True(t, existsA)

		existsB, err := managerB.SchemaExists(ctx)
		require.NoError(t, err)
		assert.True(t, existsB)
	})

	t.Run("dropping_one_schema_does_not_affect_other", func(t *testing.T) {
		// Drop schema A
		err := managerA.DropSchema(ctx)
		require.NoError(t, err)

		// A should not exist
		existsA, err := managerA.SchemaExists(ctx)
		require.NoError(t, err)
		assert.False(t, existsA)

		// B should still exist
		existsB, err := managerB.SchemaExists(ctx)
		require.NoError(t, err)
		assert.True(t, existsB, "schema B should still exist after dropping A")
	})

	// Cleanup
	managerB.DropSchema(ctx)
}

// TestIntegration_SchemaManager_VersionUpgrade tests schema version upgrade behavior.
func TestIntegration_SchemaManager_VersionUpgrade(t *testing.T) {
	// Setup test container
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schemaConfig, err := NewSchemaConfig("versiontest")
	require.NoError(t, err)

	manager := NewSchemaManager(pgContainer.db, schemaConfig)

	t.Run("creates_schema_with_version_comment", func(t *testing.T) {
		err := manager.EnsureSchema(ctx)
		require.NoError(t, err)

		// Query the table comment directly
		var comment string
		err = pgContainer.db.QueryRowContext(ctx, `
			SELECT obj_description(c.oid) AS comment
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relname = 'versiontest_hookd_subscriptions'
			  AND n.nspname = 'public'
		`).Scan(&comment)
		require.NoError(t, err)

		// Verify comment contains version
		assert.Contains(t, comment, "go-hookd schema v")
		assert.Contains(t, comment, SchemaVersion)
	})

	t.Run("detects_version_mismatch", func(t *testing.T) {
		// Manually update the comment to an older version
		_, err := pgContainer.db.ExecContext(ctx, `
			COMMENT ON TABLE versiontest_hookd_subscriptions IS 'go-hookd schema v0.1.0 - outdated'
		`)
		require.NoError(t, err)

		// Get schema info should show needs upgrade
		info, err := manager.GetSchemaInfo(ctx)
		require.NoError(t, err)

		assert.True(t, info.Exists)
		assert.Equal(t, "0.1.0", info.Version)
		assert.True(t, info.NeedsUpgrade)
	})

	t.Run("ensure_schema_upgrades_when_version_mismatch", func(t *testing.T) {
		// EnsureSchema should drop and recreate
		err := manager.EnsureSchema(ctx)
		require.NoError(t, err)

		// Version should now match
		info, err := manager.GetSchemaInfo(ctx)
		require.NoError(t, err)

		assert.Equal(t, SchemaVersion, info.Version)
		assert.False(t, info.NeedsUpgrade)
	})

	// Cleanup
	manager.DropSchema(ctx)
}

// TestIntegration_SchemaManager_Concurrent tests concurrent access to SchemaManager.
func TestIntegration_SchemaManager_Concurrent(t *testing.T) {
	// Setup test container
	pgContainer := SetupPostgresContainer(t)
	defer pgContainer.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schemaConfig, err := NewSchemaConfig("concurrent")
	require.NoError(t, err)

	manager := NewSchemaManager(pgContainer.db, schemaConfig)

	// First ensure schema exists
	err = manager.EnsureSchema(ctx)
	require.NoError(t, err)

	t.Run("concurrent_schema_exists_calls", func(t *testing.T) {
		const goroutines = 10
		errCh := make(chan error, goroutines)

		for i := 0; i < goroutines; i++ {
			go func() {
				exists, err := manager.SchemaExists(ctx)
				if err != nil {
					errCh <- err
					return
				}
				if !exists {
					errCh <- assert.AnError
					return
				}
				errCh <- nil
			}()
		}

		for i := 0; i < goroutines; i++ {
			err := <-errCh
			assert.NoError(t, err)
		}
	})

	t.Run("concurrent_get_schema_info_calls", func(t *testing.T) {
		const goroutines = 10
		errCh := make(chan error, goroutines)

		for i := 0; i < goroutines; i++ {
			go func() {
				info, err := manager.GetSchemaInfo(ctx)
				if err != nil {
					errCh <- err
					return
				}
				if info.Prefix != "concurrent" {
					errCh <- assert.AnError
					return
				}
				errCh <- nil
			}()
		}

		for i := 0; i < goroutines; i++ {
			err := <-errCh
			assert.NoError(t, err)
		}
	})

	// Cleanup
	manager.DropSchema(ctx)
}
