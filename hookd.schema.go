// Package hookd provides webhook management functionality.
//
// This file contains the SchemaConfig type which manages database schema naming
// for multi-service isolation. Each service using go-hookd can have its own
// table prefix to prevent namespace collisions when sharing a PostgreSQL database.
//
// Excellence. Always.
package hookd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/itsatony/go-cuserr"
)

// Schema configuration constants.
const (
	// MaxPrefixLength is the maximum allowed length for a table prefix.
	// PostgreSQL table name limit is 63 chars; we reserve space for table names.
	MaxPrefixLength = 32

	// SchemaVersion is the current schema version embedded in table comments.
	// Used to detect and handle schema upgrades.
	SchemaVersion = "0.6.0"

	// HookdTableInfix is the infix used in all table names: {prefix}_hookd_{table}
	HookdTableInfix = "hookd"
)

// Table name suffixes (used with prefix to build full table names).
const (
	// TableSuffixSubscriptions is the suffix for the subscriptions table.
	TableSuffixSubscriptions = "subscriptions"

	// TableSuffixDeliveries is the suffix for the deliveries table.
	TableSuffixDeliveries = "deliveries"

	// TableSuffixDeliveryAttempts is the suffix for the delivery_attempts table.
	TableSuffixDeliveryAttempts = "delivery_attempts"

	// TableSuffixIdempotencyStore is the suffix for the idempotency_store table.
	TableSuffixIdempotencyStore = "idempotency_store"

	// TableSuffixCircuitBreakerState is the suffix for the circuit_breaker_state table.
	TableSuffixCircuitBreakerState = "circuit_breaker_state"
)

// Function and trigger name suffixes.
const (
	// FuncSuffixUpdateUpdatedAt is the suffix for the update_updated_at function.
	FuncSuffixUpdateUpdatedAt = "update_updated_at"

	// FuncSuffixCleanupIdempotency is the suffix for the cleanup_expired_idempotency function.
	FuncSuffixCleanupIdempotency = "cleanup_expired_idempotency"
)

// Error messages for schema configuration.
const (
	// ErrMsgPrefixRequired is the error message when no prefix is provided.
	ErrMsgPrefixRequired = "table_prefix is required - specify hookd.WithTablePrefix(\"yourprefix\")"

	// ErrMsgInvalidPrefix is the error message for invalid prefix format.
	ErrMsgInvalidPrefix = "table_prefix must start with a letter and contain only lowercase letters, numbers, and underscores"

	// ErrMsgPrefixTooLong is the error message when prefix exceeds maximum length.
	ErrMsgPrefixTooLong = "table_prefix exceeds maximum length of %d characters"

	// ErrMsgPrefixSQLKeyword is the error message when prefix is a SQL keyword.
	ErrMsgPrefixSQLKeyword = "table_prefix '%s' is a SQL reserved keyword"
)

// prefixPattern validates the prefix format: starts with letter, lowercase alphanumeric + underscore.
var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// sqlKeywords is a list of SQL reserved keywords that cannot be used as prefixes.
var sqlKeywords = []string{
	"select", "insert", "update", "delete", "drop", "create", "alter",
	"table", "schema", "index", "trigger", "function", "view",
	"database", "grant", "revoke", "commit", "rollback", "transaction",
}

// SchemaConfig holds database schema naming configuration.
// It generates table, index, function, and trigger names with a consistent prefix.
//
// Thread Safety: SchemaConfig is immutable after creation and safe for concurrent use.
type SchemaConfig struct {
	prefix string // The user-provided prefix (e.g., "myservice")
}

// NewSchemaConfig creates a new SchemaConfig with the given prefix.
// The prefix must:
//   - Not be empty (required for multi-service isolation)
//   - Start with a lowercase letter
//   - Contain only lowercase letters, numbers, and underscores
//   - Not exceed MaxPrefixLength characters
//   - Not be a SQL reserved keyword
//
// Example:
//
//	config, err := NewSchemaConfig("myservice")
//	// Tables will be: myservice_hookd_subscriptions, myservice_hookd_deliveries, etc.
func NewSchemaConfig(prefix string) (*SchemaConfig, error) {
	if err := ValidatePrefix(prefix); err != nil {
		return nil, err
	}
	return &SchemaConfig{prefix: prefix}, nil
}

// ValidatePrefix validates a table prefix according to go-hookd requirements.
// Returns nil if valid, or a cuserr validation error if invalid.
func ValidatePrefix(prefix string) error {
	// Check if prefix is empty
	if prefix == "" {
		return cuserr.NewValidationError("table_prefix", ErrMsgPrefixRequired)
	}

	// Check length
	if len(prefix) > MaxPrefixLength {
		return cuserr.NewValidationError("table_prefix",
			fmt.Sprintf(ErrMsgPrefixTooLong, MaxPrefixLength))
	}

	// Check format (lowercase alphanumeric + underscore, starts with letter)
	if !prefixPattern.MatchString(prefix) {
		return cuserr.NewValidationError("table_prefix", ErrMsgInvalidPrefix)
	}

	// Check for SQL keywords
	prefixLower := strings.ToLower(prefix)
	for _, keyword := range sqlKeywords {
		if prefixLower == keyword {
			return cuserr.NewValidationError("table_prefix",
				fmt.Sprintf(ErrMsgPrefixSQLKeyword, prefix))
		}
	}

	return nil
}

// Prefix returns the configured prefix.
func (s *SchemaConfig) Prefix() string {
	return s.prefix
}

// =============================================================================
// TABLE NAMES
// =============================================================================

// TableSubscriptions returns the full subscriptions table name.
// Format: {prefix}_hookd_subscriptions
func (s *SchemaConfig) TableSubscriptions() string {
	return s.tableName(TableSuffixSubscriptions)
}

// TableDeliveries returns the full deliveries table name.
// Format: {prefix}_hookd_deliveries
func (s *SchemaConfig) TableDeliveries() string {
	return s.tableName(TableSuffixDeliveries)
}

// TableDeliveryAttempts returns the full delivery_attempts table name.
// Format: {prefix}_hookd_delivery_attempts
func (s *SchemaConfig) TableDeliveryAttempts() string {
	return s.tableName(TableSuffixDeliveryAttempts)
}

// TableIdempotencyStore returns the full idempotency_store table name.
// Format: {prefix}_hookd_idempotency_store
func (s *SchemaConfig) TableIdempotencyStore() string {
	return s.tableName(TableSuffixIdempotencyStore)
}

// TableCircuitBreakerState returns the full circuit_breaker_state table name.
// Format: {prefix}_hookd_circuit_breaker_state
func (s *SchemaConfig) TableCircuitBreakerState() string {
	return s.tableName(TableSuffixCircuitBreakerState)
}

// tableName builds a full table name from the suffix.
// Format: {prefix}_hookd_{suffix}
func (s *SchemaConfig) tableName(suffix string) string {
	return fmt.Sprintf("%s_%s_%s", s.prefix, HookdTableInfix, suffix)
}

// =============================================================================
// INDEX NAMES
// =============================================================================

// IndexName generates an index name for a table and column(s).
// Format: idx_{prefix}_hookd_{table}_{columns}
//
// Example:
//
//	schema.IndexName("subscriptions", "tenant_id")
//	// Returns: idx_myservice_hookd_subscriptions_tenant_id
func (s *SchemaConfig) IndexName(table, columns string) string {
	return fmt.Sprintf("idx_%s_%s_%s_%s", s.prefix, HookdTableInfix, table, columns)
}

// UniqueIndexName generates a unique index name for a table and column(s).
// Format: idx_{prefix}_hookd_{table}_{columns}_unique
//
// Example:
//
//	schema.UniqueIndexName("subscriptions", "tenant_url")
//	// Returns: idx_myservice_hookd_subscriptions_tenant_url_unique
func (s *SchemaConfig) UniqueIndexName(table, columns string) string {
	return fmt.Sprintf("idx_%s_%s_%s_%s_unique", s.prefix, HookdTableInfix, table, columns)
}

// =============================================================================
// CONSTRAINT NAMES
// =============================================================================

// CheckConstraintName generates a check constraint name.
// Format: chk_{prefix}_hookd_{table}_{rule}
//
// Example:
//
//	schema.CheckConstraintName("subscriptions", "status")
//	// Returns: chk_myservice_hookd_subscriptions_status
func (s *SchemaConfig) CheckConstraintName(table, rule string) string {
	return fmt.Sprintf("chk_%s_%s_%s_%s", s.prefix, HookdTableInfix, table, rule)
}

// ForeignKeyName generates a foreign key constraint name.
// Format: fk_{prefix}_hookd_{table}_{reference}
//
// Example:
//
//	schema.ForeignKeyName("deliveries", "subscription")
//	// Returns: fk_myservice_hookd_deliveries_subscription
func (s *SchemaConfig) ForeignKeyName(table, reference string) string {
	return fmt.Sprintf("fk_%s_%s_%s_%s", s.prefix, HookdTableInfix, table, reference)
}

// =============================================================================
// FUNCTION NAMES
// =============================================================================

// FuncUpdateUpdatedAt returns the update_updated_at function name.
// Format: {prefix}_hookd_update_updated_at
func (s *SchemaConfig) FuncUpdateUpdatedAt() string {
	return s.functionName(FuncSuffixUpdateUpdatedAt)
}

// FuncCleanupIdempotency returns the cleanup_expired_idempotency function name.
// Format: {prefix}_hookd_cleanup_expired_idempotency
func (s *SchemaConfig) FuncCleanupIdempotency() string {
	return s.functionName(FuncSuffixCleanupIdempotency)
}

// functionName builds a full function name from the suffix.
// Format: {prefix}_hookd_{suffix}
func (s *SchemaConfig) functionName(suffix string) string {
	return fmt.Sprintf("%s_%s_%s", s.prefix, HookdTableInfix, suffix)
}

// =============================================================================
// TRIGGER NAMES
// =============================================================================

// TriggerName generates a trigger name for a table and event.
// Format: trg_{prefix}_hookd_{table}_{event}
//
// Example:
//
//	schema.TriggerName("subscriptions", "updated_at")
//	// Returns: trg_myservice_hookd_subscriptions_updated_at
func (s *SchemaConfig) TriggerName(table, event string) string {
	return fmt.Sprintf("trg_%s_%s_%s_%s", s.prefix, HookdTableInfix, table, event)
}

// =============================================================================
// HELPER METHODS
// =============================================================================

// AllTableNames returns all table names managed by this schema config.
// Useful for cleanup and schema verification operations.
func (s *SchemaConfig) AllTableNames() []string {
	return []string{
		s.TableSubscriptions(),
		s.TableDeliveries(),
		s.TableDeliveryAttempts(),
		s.TableIdempotencyStore(),
		s.TableCircuitBreakerState(),
	}
}

// AllFunctionNames returns all function names managed by this schema config.
func (s *SchemaConfig) AllFunctionNames() []string {
	return []string{
		s.FuncUpdateUpdatedAt(),
		s.FuncCleanupIdempotency(),
	}
}

// SchemaVersionComment returns the comment to be added to the main table
// for schema version tracking.
// Format: go-hookd schema v{version}
func (s *SchemaConfig) SchemaVersionComment() string {
	return fmt.Sprintf("go-hookd schema v%s", SchemaVersion)
}
