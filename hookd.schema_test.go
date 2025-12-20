// Package hookd provides webhook management functionality.
//
// This file contains tests for the SchemaConfig type and prefix validation.
package hookd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// VALIDATION TESTS
// =============================================================================

func TestNewSchemaConfig_ValidPrefixes(t *testing.T) {
	testCases := []struct {
		name   string
		prefix string
	}{
		{"simple lowercase", "myservice"},
		{"with underscore", "my_service"},
		{"with numbers", "service123"},
		{"mixed", "my_service_v2"},
		{"single letter", "a"},
		{"minimum valid", "a1"},
		{"max length", strings.Repeat("a", MaxPrefixLength)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := NewSchemaConfig(tc.prefix)
			require.NoError(t, err, "prefix '%s' should be valid", tc.prefix)
			assert.Equal(t, tc.prefix, config.Prefix())
		})
	}
}

func TestNewSchemaConfig_InvalidPrefixes(t *testing.T) {
	testCases := []struct {
		name        string
		prefix      string
		errContains string
	}{
		{"empty prefix", "", ErrMsgPrefixRequired},
		{"starts with number", "1service", ErrMsgInvalidPrefix},
		{"starts with underscore", "_service", ErrMsgInvalidPrefix},
		{"uppercase letters", "MyService", ErrMsgInvalidPrefix},
		{"mixed case", "myService", ErrMsgInvalidPrefix},
		{"contains hyphen", "my-service", ErrMsgInvalidPrefix},
		{"contains dot", "my.service", ErrMsgInvalidPrefix},
		{"contains space", "my service", ErrMsgInvalidPrefix},
		{"contains special char", "my@service", ErrMsgInvalidPrefix},
		{"too long", strings.Repeat("a", MaxPrefixLength+1), "exceeds maximum length"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := NewSchemaConfig(tc.prefix)
			require.Error(t, err, "prefix '%s' should be invalid", tc.prefix)
			assert.Nil(t, config)
			assert.Contains(t, err.Error(), tc.errContains)
		})
	}
}

func TestNewSchemaConfig_SQLKeywords(t *testing.T) {
	keywords := []string{
		"select", "insert", "update", "delete", "drop", "create", "alter",
		"table", "schema", "index", "trigger", "function", "view",
		"database", "grant", "revoke", "commit", "rollback", "transaction",
	}

	for _, keyword := range keywords {
		t.Run(keyword, func(t *testing.T) {
			config, err := NewSchemaConfig(keyword)
			require.Error(t, err, "SQL keyword '%s' should be rejected", keyword)
			assert.Nil(t, config)
			assert.Contains(t, err.Error(), "SQL reserved keyword")
		})
	}
}

func TestValidatePrefix_DirectCall(t *testing.T) {
	// Valid
	assert.NoError(t, ValidatePrefix("valid"))
	assert.NoError(t, ValidatePrefix("valid_prefix"))

	// Invalid
	assert.Error(t, ValidatePrefix(""))
	assert.Error(t, ValidatePrefix("Invalid"))
	assert.Error(t, ValidatePrefix("select"))
}

// =============================================================================
// TABLE NAME TESTS
// =============================================================================

func TestSchemaConfig_TableNames(t *testing.T) {
	config, err := NewSchemaConfig("myservice")
	require.NoError(t, err)

	testCases := []struct {
		name     string
		method   func() string
		expected string
	}{
		{"subscriptions", config.TableSubscriptions, "myservice_hookd_subscriptions"},
		{"deliveries", config.TableDeliveries, "myservice_hookd_deliveries"},
		{"delivery_attempts", config.TableDeliveryAttempts, "myservice_hookd_delivery_attempts"},
		{"idempotency_store", config.TableIdempotencyStore, "myservice_hookd_idempotency_store"},
		{"circuit_breaker_state", config.TableCircuitBreakerState, "myservice_hookd_circuit_breaker_state"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.method())
		})
	}
}

func TestSchemaConfig_TableNames_DifferentPrefixes(t *testing.T) {
	prefixes := []string{"servicea", "serviceb", "prod", "staging", "test_env"}

	for _, prefix := range prefixes {
		t.Run(prefix, func(t *testing.T) {
			config, err := NewSchemaConfig(prefix)
			require.NoError(t, err)

			// All table names should start with prefix_hookd_
			expectedPrefix := prefix + "_hookd_"
			assert.True(t, strings.HasPrefix(config.TableSubscriptions(), expectedPrefix))
			assert.True(t, strings.HasPrefix(config.TableDeliveries(), expectedPrefix))
			assert.True(t, strings.HasPrefix(config.TableDeliveryAttempts(), expectedPrefix))
			assert.True(t, strings.HasPrefix(config.TableIdempotencyStore(), expectedPrefix))
			assert.True(t, strings.HasPrefix(config.TableCircuitBreakerState(), expectedPrefix))
		})
	}
}

func TestSchemaConfig_AllTableNames(t *testing.T) {
	config, err := NewSchemaConfig("test")
	require.NoError(t, err)

	tables := config.AllTableNames()
	assert.Len(t, tables, 5)
	assert.Contains(t, tables, "test_hookd_subscriptions")
	assert.Contains(t, tables, "test_hookd_deliveries")
	assert.Contains(t, tables, "test_hookd_delivery_attempts")
	assert.Contains(t, tables, "test_hookd_idempotency_store")
	assert.Contains(t, tables, "test_hookd_circuit_breaker_state")
}

// =============================================================================
// INDEX NAME TESTS
// =============================================================================

func TestSchemaConfig_IndexName(t *testing.T) {
	config, err := NewSchemaConfig("myservice")
	require.NoError(t, err)

	testCases := []struct {
		table    string
		columns  string
		expected string
	}{
		{"subscriptions", "tenant_id", "idx_myservice_hookd_subscriptions_tenant_id"},
		{"subscriptions", "status", "idx_myservice_hookd_subscriptions_status"},
		{"deliveries", "subscription_id", "idx_myservice_hookd_deliveries_subscription_id"},
		{"deliveries", "status_next_attempt", "idx_myservice_hookd_deliveries_status_next_attempt"},
	}

	for _, tc := range testCases {
		t.Run(tc.table+"_"+tc.columns, func(t *testing.T) {
			assert.Equal(t, tc.expected, config.IndexName(tc.table, tc.columns))
		})
	}
}

func TestSchemaConfig_UniqueIndexName(t *testing.T) {
	config, err := NewSchemaConfig("myservice")
	require.NoError(t, err)

	result := config.UniqueIndexName("subscriptions", "tenant_url")
	assert.Equal(t, "idx_myservice_hookd_subscriptions_tenant_url_unique", result)
}

// =============================================================================
// CONSTRAINT NAME TESTS
// =============================================================================

func TestSchemaConfig_CheckConstraintName(t *testing.T) {
	config, err := NewSchemaConfig("myservice")
	require.NoError(t, err)

	testCases := []struct {
		table    string
		rule     string
		expected string
	}{
		{"subscriptions", "status", "chk_myservice_hookd_subscriptions_status"},
		{"subscriptions", "url", "chk_myservice_hookd_subscriptions_url"},
		{"deliveries", "status", "chk_myservice_hookd_deliveries_status"},
	}

	for _, tc := range testCases {
		t.Run(tc.table+"_"+tc.rule, func(t *testing.T) {
			assert.Equal(t, tc.expected, config.CheckConstraintName(tc.table, tc.rule))
		})
	}
}

func TestSchemaConfig_ForeignKeyName(t *testing.T) {
	config, err := NewSchemaConfig("myservice")
	require.NoError(t, err)

	testCases := []struct {
		table     string
		reference string
		expected  string
	}{
		{"deliveries", "subscription", "fk_myservice_hookd_deliveries_subscription"},
		{"delivery_attempts", "delivery", "fk_myservice_hookd_delivery_attempts_delivery"},
	}

	for _, tc := range testCases {
		t.Run(tc.table+"_"+tc.reference, func(t *testing.T) {
			assert.Equal(t, tc.expected, config.ForeignKeyName(tc.table, tc.reference))
		})
	}
}

// =============================================================================
// FUNCTION NAME TESTS
// =============================================================================

func TestSchemaConfig_FunctionNames(t *testing.T) {
	config, err := NewSchemaConfig("myservice")
	require.NoError(t, err)

	assert.Equal(t, "myservice_hookd_update_updated_at", config.FuncUpdateUpdatedAt())
	assert.Equal(t, "myservice_hookd_cleanup_expired_idempotency", config.FuncCleanupIdempotency())
}

func TestSchemaConfig_AllFunctionNames(t *testing.T) {
	config, err := NewSchemaConfig("test")
	require.NoError(t, err)

	functions := config.AllFunctionNames()
	assert.Len(t, functions, 2)
	assert.Contains(t, functions, "test_hookd_update_updated_at")
	assert.Contains(t, functions, "test_hookd_cleanup_expired_idempotency")
}

// =============================================================================
// TRIGGER NAME TESTS
// =============================================================================

func TestSchemaConfig_TriggerName(t *testing.T) {
	config, err := NewSchemaConfig("myservice")
	require.NoError(t, err)

	testCases := []struct {
		table    string
		event    string
		expected string
	}{
		{"subscriptions", "updated_at", "trg_myservice_hookd_subscriptions_updated_at"},
		{"circuit_breaker_state", "updated_at", "trg_myservice_hookd_circuit_breaker_state_updated_at"},
	}

	for _, tc := range testCases {
		t.Run(tc.table+"_"+tc.event, func(t *testing.T) {
			assert.Equal(t, tc.expected, config.TriggerName(tc.table, tc.event))
		})
	}
}

// =============================================================================
// SCHEMA VERSION TESTS
// =============================================================================

func TestSchemaConfig_SchemaVersionComment(t *testing.T) {
	config, err := NewSchemaConfig("myservice")
	require.NoError(t, err)

	comment := config.SchemaVersionComment()
	assert.Contains(t, comment, "go-hookd schema v")
	assert.Contains(t, comment, SchemaVersion)
}

// =============================================================================
// EDGE CASE TESTS
// =============================================================================

func TestSchemaConfig_Immutability(t *testing.T) {
	config, err := NewSchemaConfig("original")
	require.NoError(t, err)

	// Verify prefix cannot be modified
	prefix := config.Prefix()
	assert.Equal(t, "original", prefix)

	// Multiple calls return same value
	assert.Equal(t, config.Prefix(), config.Prefix())
	assert.Equal(t, config.TableSubscriptions(), config.TableSubscriptions())
}

func TestSchemaConfig_ConsistentNaming(t *testing.T) {
	config, err := NewSchemaConfig("myprefix")
	require.NoError(t, err)

	// All generated names should contain the prefix and hookd infix
	allTables := config.AllTableNames()
	for _, table := range allTables {
		assert.Contains(t, table, "myprefix")
		assert.Contains(t, table, "hookd")
	}

	allFunctions := config.AllFunctionNames()
	for _, fn := range allFunctions {
		assert.Contains(t, fn, "myprefix")
		assert.Contains(t, fn, "hookd")
	}
}

func TestSchemaConfig_DifferentPrefixesDifferentNames(t *testing.T) {
	configA, err := NewSchemaConfig("servicea")
	require.NoError(t, err)

	configB, err := NewSchemaConfig("serviceb")
	require.NoError(t, err)

	// All table names should be different
	assert.NotEqual(t, configA.TableSubscriptions(), configB.TableSubscriptions())
	assert.NotEqual(t, configA.TableDeliveries(), configB.TableDeliveries())

	// All function names should be different
	assert.NotEqual(t, configA.FuncUpdateUpdatedAt(), configB.FuncUpdateUpdatedAt())

	// Index names should be different
	assert.NotEqual(t,
		configA.IndexName("subscriptions", "tenant_id"),
		configB.IndexName("subscriptions", "tenant_id"))
}

// =============================================================================
// BENCHMARK TESTS
// =============================================================================

func BenchmarkSchemaConfig_TableNames(b *testing.B) {
	config, _ := NewSchemaConfig("benchmark")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = config.TableSubscriptions()
		_ = config.TableDeliveries()
		_ = config.TableDeliveryAttempts()
		_ = config.TableIdempotencyStore()
		_ = config.TableCircuitBreakerState()
	}
}

func BenchmarkValidatePrefix(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidatePrefix("valid_prefix_123")
	}
}
