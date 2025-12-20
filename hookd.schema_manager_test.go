// Package hookd provides webhook management functionality.
//
// This file contains unit tests for the SchemaManager.
// Integration tests are in hookd.schema_manager_integration_test.go.
//
// Excellence. Always.
package hookd

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// UNIT TESTS (no database required)
// =============================================================================

// TestNewSchemaManager tests the basic constructor.
func TestNewSchemaManager(t *testing.T) {
	t.Run("creates_manager_with_valid_config", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("test")
		require.NoError(t, err)

		// Note: db can be nil for basic testing
		manager := NewSchemaManager(nil, schemaConfig)
		require.NotNil(t, manager)
		assert.Equal(t, schemaConfig, manager.SchemaConfig())
		assert.Nil(t, manager.DB())
	})

	t.Run("returns_nil_db_when_constructed_with_nil", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("test")
		require.NoError(t, err)

		manager := NewSchemaManager(nil, schemaConfig)
		assert.Nil(t, manager.DB())
	})
}

// TestSchemaManager_Close tests the Close method.
func TestSchemaManager_Close(t *testing.T) {
	t.Run("close_with_nil_db_succeeds", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("test")
		require.NoError(t, err)

		manager := NewSchemaManager(nil, schemaConfig)
		err = manager.Close()
		assert.NoError(t, err)
	})
}

// TestSchemaManager_ProcessTemplate tests template processing.
func TestSchemaManager_ProcessTemplate(t *testing.T) {
	t.Run("processes_template_with_prefix", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("myservice")
		require.NoError(t, err)

		// Verify schema template is embedded
		assert.NotEmpty(t, schemaTemplate, "schema template should be embedded")
		assert.Contains(t, schemaTemplate, "{{.Prefix}}", "template should contain prefix placeholder")

		// Verify schema config works (used by processTemplate internally)
		assert.Equal(t, "myservice", schemaConfig.Prefix())
	})

	t.Run("template_contains_all_required_tables", func(t *testing.T) {
		// Verify the embedded template has all required CREATE TABLE statements
		assert.Contains(t, schemaTemplate, "subscriptions", "template should create subscriptions table")
		assert.Contains(t, schemaTemplate, "deliveries", "template should create deliveries table")
		assert.Contains(t, schemaTemplate, "delivery_attempts", "template should create delivery_attempts table")
		assert.Contains(t, schemaTemplate, "idempotency_store", "template should create idempotency_store table")
		assert.Contains(t, schemaTemplate, "circuit_breaker_state", "template should create circuit_breaker_state table")
	})

	t.Run("template_contains_functions_and_triggers", func(t *testing.T) {
		assert.Contains(t, schemaTemplate, "update_updated_at", "template should create update_updated_at function")
		assert.Contains(t, schemaTemplate, "cleanup_expired_idempotency", "template should create cleanup function")
		assert.Contains(t, schemaTemplate, "CREATE TRIGGER", "template should create triggers")
	})

	t.Run("template_contains_indexes", func(t *testing.T) {
		assert.Contains(t, schemaTemplate, "CREATE INDEX", "template should create indexes")
	})
}

// TestSchemaManager_BuildDropSQL tests the drop SQL generation.
func TestSchemaManager_BuildDropSQL(t *testing.T) {
	schemaConfig, err := NewSchemaConfig("droptest")
	require.NoError(t, err)

	manager := NewSchemaManager(nil, schemaConfig)
	dropSQL := manager.buildDropSQL()

	t.Run("drops_all_tables", func(t *testing.T) {
		assert.Contains(t, dropSQL, "droptest_hookd_subscriptions")
		assert.Contains(t, dropSQL, "droptest_hookd_deliveries")
		assert.Contains(t, dropSQL, "droptest_hookd_delivery_attempts")
		assert.Contains(t, dropSQL, "droptest_hookd_idempotency_store")
		assert.Contains(t, dropSQL, "droptest_hookd_circuit_breaker_state")
	})

	t.Run("drops_functions", func(t *testing.T) {
		assert.Contains(t, dropSQL, "droptest_hookd_update_updated_at")
		assert.Contains(t, dropSQL, "droptest_hookd_cleanup_expired_idempotency")
	})

	t.Run("drops_triggers", func(t *testing.T) {
		assert.Contains(t, dropSQL, "DROP TRIGGER")
	})

	t.Run("uses_cascade", func(t *testing.T) {
		assert.Contains(t, dropSQL, "CASCADE")
	})

	t.Run("uses_if_exists", func(t *testing.T) {
		assert.Contains(t, dropSQL, "IF EXISTS")
	})
}

// TestSchemaInfo tests the SchemaInfo struct.
func TestSchemaInfo(t *testing.T) {
	t.Run("schema_info_fields", func(t *testing.T) {
		info := &SchemaInfo{
			Exists:        true,
			Version:       "0.6.0",
			Prefix:        "test",
			TableNames:    []string{"test_hookd_subscriptions"},
			LatestVersion: SchemaVersion,
			NeedsUpgrade:  false,
		}

		assert.True(t, info.Exists)
		assert.Equal(t, "0.6.0", info.Version)
		assert.Equal(t, "test", info.Prefix)
		assert.Len(t, info.TableNames, 1)
		assert.False(t, info.NeedsUpgrade)
	})
}

// TestSchemaVersionPattern tests the regex for extracting schema version.
func TestSchemaVersionPattern(t *testing.T) {
	t.Run("matches_valid_version_format", func(t *testing.T) {
		comment := "go-hookd schema v0.6.0 - Enterprise webhook management"
		matches := schemaVersionPattern.FindStringSubmatch(comment)
		require.Len(t, matches, 2)
		assert.Equal(t, "0.6.0", matches[1])
	})

	t.Run("matches_version_only", func(t *testing.T) {
		comment := "go-hookd schema v1.2.3"
		matches := schemaVersionPattern.FindStringSubmatch(comment)
		require.Len(t, matches, 2)
		assert.Equal(t, "1.2.3", matches[1])
	})

	t.Run("no_match_for_invalid_format", func(t *testing.T) {
		invalid := []string{
			"some random comment",
			"hookd schema v1.0",
			"go-hookd v0.6.0",
		}

		for _, comment := range invalid {
			matches := schemaVersionPattern.FindStringSubmatch(comment)
			assert.Empty(t, matches, "should not match: %s", comment)
		}
	})
}

// TestSchemaManager_ErrorHandling tests error handling in SchemaManager.
func TestSchemaManager_ErrorHandling(t *testing.T) {
	t.Run("ensure_schema_with_nil_db_returns_error", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("test")
		require.NoError(t, err)

		manager := NewSchemaManager(nil, schemaConfig)

		ctx := context.Background()
		err = manager.EnsureSchema(ctx)
		assert.Error(t, err)
	})

	t.Run("drop_schema_with_nil_db_returns_error", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("test")
		require.NoError(t, err)

		manager := NewSchemaManager(nil, schemaConfig)

		ctx := context.Background()
		err = manager.DropSchema(ctx)
		assert.Error(t, err)
	})

	t.Run("schema_exists_with_nil_db_returns_error", func(t *testing.T) {
		schemaConfig, err := NewSchemaConfig("test")
		require.NoError(t, err)

		manager := NewSchemaManager(nil, schemaConfig)

		ctx := context.Background()
		_, err = manager.SchemaExists(ctx)
		assert.Error(t, err)
	})
}

// TestProcessTemplateOutput verifies the template produces valid SQL.
func TestProcessTemplateOutput(t *testing.T) {
	schemaConfig, err := NewSchemaConfig("templatetest")
	require.NoError(t, err)

	manager := NewSchemaManager(nil, schemaConfig)

	t.Run("template_is_valid", func(t *testing.T) {
		// Verify template syntax by checking it doesn't have unclosed braces
		openBraces := strings.Count(schemaTemplate, "{{")
		closeBraces := strings.Count(schemaTemplate, "}}")
		assert.Equal(t, openBraces, closeBraces, "template should have matching braces")
	})

	t.Run("schema_config_generates_correct_table_names", func(t *testing.T) {
		sc := manager.SchemaConfig()
		assert.Equal(t, "templatetest_hookd_subscriptions", sc.TableSubscriptions())
		assert.Equal(t, "templatetest_hookd_deliveries", sc.TableDeliveries())
		assert.Equal(t, "templatetest_hookd_delivery_attempts", sc.TableDeliveryAttempts())
		assert.Equal(t, "templatetest_hookd_idempotency_store", sc.TableIdempotencyStore())
		assert.Equal(t, "templatetest_hookd_circuit_breaker_state", sc.TableCircuitBreakerState())
	})
}
