// Package hookd provides webhook management functionality.
//
// This file implements the SchemaManager for database schema setup.
// The SchemaManager creates or replaces the database schema for go-hookd
// using a configurable table prefix for multi-service isolation.
//
// Excellence. Always.
package hookd

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/itsatony/go-cuserr"
)

//go:embed schema.sql
var schemaTemplate string

// schemaVersionPattern extracts version from table comment.
// Expected format: "go-hookd schema v0.6.0 - ..."
var schemaVersionPattern = regexp.MustCompile(`go-hookd schema v(\d+\.\d+\.\d+)`)

// SchemaManager handles database schema setup for go-hookd.
// It creates or replaces tables with the configured prefix for multi-service isolation.
//
// Thread Safety: SchemaManager is safe for concurrent use. The underlying sql.DB
// handles connection pooling and concurrent access automatically.
type SchemaManager struct {
	db           *sql.DB
	schemaConfig *SchemaConfig
}

// NewSchemaManager creates a new SchemaManager with the given database connection and schema config.
//
// The schemaConfig determines the table prefix used for all database objects.
// Each service sharing a PostgreSQL database should use a unique prefix.
//
// Example:
//
//	db, _ := sql.Open("postgres", connectionString)
//	schemaConfig, _ := hookd.NewSchemaConfig("myservice")
//	schemaMgr := hookd.NewSchemaManager(db, schemaConfig)
func NewSchemaManager(db *sql.DB, schemaConfig *SchemaConfig) *SchemaManager {
	return &SchemaManager{
		db:           db,
		schemaConfig: schemaConfig,
	}
}

// NewSchemaManagerFromURL creates a new SchemaManager by connecting to the given database URL.
//
// The schemaConfig determines the table prefix used for all database objects.
//
// Example:
//
//	schemaConfig, _ := hookd.NewSchemaConfig("myservice")
//	schemaMgr, err := hookd.NewSchemaManagerFromURL(dbURL, schemaConfig)
func NewSchemaManagerFromURL(dbURL string, schemaConfig *SchemaConfig) (*SchemaManager, error) {
	if schemaConfig == nil {
		return nil, cuserr.NewValidationError("schema_config", "schema configuration is required")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "open_connection"),
		)
	}

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "ping"),
		)
	}

	return &SchemaManager{
		db:           db,
		schemaConfig: schemaConfig,
	}, nil
}

// EnsureSchema creates or replaces the database schema for the configured prefix.
//
// Behavior:
//   - If tables don't exist: creates them
//   - If tables exist with older version: drops and recreates (no data migration)
//   - If tables exist with current version: does nothing (idempotent)
//
// This is a destructive operation for schema upgrades - existing data will be lost
// if the schema version changes. This is acceptable for pre-release versions.
//
// Example:
//
//	if err := schemaMgr.EnsureSchema(ctx); err != nil {
//	    log.Fatal("schema setup failed:", err)
//	}
func (m *SchemaManager) EnsureSchema(ctx context.Context) error {
	if m.db == nil {
		return cuserr.NewValidationError("database", "database connection is required")
	}

	// Check if schema exists and get current version
	exists, currentVersion, err := m.getSchemaVersion(ctx)
	if err != nil {
		return err
	}

	// If schema exists with current version, nothing to do
	if exists && currentVersion == SchemaVersion {
		return nil
	}

	// Process the template
	sql, err := m.processTemplate()
	if err != nil {
		return err
	}

	// Execute the schema SQL
	if _, err := m.db.ExecContext(ctx, sql); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "create_schema"),
			cuserr.WithMetadata("prefix", m.schemaConfig.Prefix()),
		)
	}

	return nil
}

// DropSchema drops all tables for the configured prefix.
// This is useful for testing and cleanup operations.
//
// WARNING: This is a destructive operation that permanently deletes all data.
func (m *SchemaManager) DropSchema(ctx context.Context) error {
	if m.db == nil {
		return cuserr.NewValidationError("database", "database connection is required")
	}

	// Build DROP statements for all objects
	dropSQL := m.buildDropSQL()

	// Execute the drop SQL
	if _, err := m.db.ExecContext(ctx, dropSQL); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "drop_schema"),
			cuserr.WithMetadata("prefix", m.schemaConfig.Prefix()),
		)
	}

	return nil
}

// SchemaExists checks if schema tables exist for the configured prefix.
func (m *SchemaManager) SchemaExists(ctx context.Context) (bool, error) {
	exists, _, err := m.getSchemaVersion(ctx)
	return exists, err
}

// GetSchemaVersion returns the current schema version if tables exist.
// Returns empty string if tables don't exist.
func (m *SchemaManager) GetSchemaVersion(ctx context.Context) (string, error) {
	exists, version, err := m.getSchemaVersion(ctx)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	return version, nil
}

// SchemaConfig returns the schema configuration for this manager.
func (m *SchemaManager) SchemaConfig() *SchemaConfig {
	return m.schemaConfig
}

// Close closes the database connection.
// Call this when done with the SchemaManager if it was created via NewSchemaManagerFromURL.
func (m *SchemaManager) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

// DB returns the underlying database connection.
// This is useful for creating a PostgresRepository with the same connection.
func (m *SchemaManager) DB() *sql.DB {
	return m.db
}

// =============================================================================
// INTERNAL METHODS
// =============================================================================

// getSchemaVersion checks if the schema exists and returns the version from table comment.
func (m *SchemaManager) getSchemaVersion(ctx context.Context) (exists bool, version string, err error) {
	if m.db == nil {
		return false, "", cuserr.NewValidationError("database", "database connection is required")
	}

	// Check if the subscriptions table exists (primary table)
	tableName := m.schemaConfig.TableSubscriptions()

	// Query to check if table exists and get its comment
	query := `
		SELECT obj_description(c.oid) AS comment
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = $1
		  AND n.nspname = 'public'
		  AND c.relkind = 'r'
	`

	var comment sql.NullString
	err = m.db.QueryRowContext(ctx, query, tableName).Scan(&comment)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	if err != nil {
		return false, "", cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "check_schema_version"),
			cuserr.WithMetadata("table", tableName),
		)
	}

	// Extract version from comment
	if comment.Valid {
		matches := schemaVersionPattern.FindStringSubmatch(comment.String)
		if len(matches) > 1 {
			return true, matches[1], nil
		}
	}

	// Table exists but no version comment - assume very old version
	return true, "0.0.0", nil
}

// processTemplate processes the schema.sql template with the configured prefix.
func (m *SchemaManager) processTemplate() (string, error) {
	tmpl, err := template.New("schema").Parse(schemaTemplate)
	if err != nil {
		return "", cuserr.NewInternalError("template", err,
			cuserr.WithMetadata("operation", "parse_template"),
		)
	}

	data := struct {
		Prefix string
	}{
		Prefix: m.schemaConfig.Prefix(),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", cuserr.NewInternalError("template", err,
			cuserr.WithMetadata("operation", "execute_template"),
			cuserr.WithMetadata("prefix", m.schemaConfig.Prefix()),
		)
	}

	return buf.String(), nil
}

// buildDropSQL builds DROP statements for all schema objects.
func (m *SchemaManager) buildDropSQL() string {
	var statements []string

	// Drop triggers first (they depend on tables and functions)
	statements = append(statements,
		fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s",
			m.schemaConfig.TriggerName("subscriptions", "updated_at"),
			m.schemaConfig.TableSubscriptions()),
		fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s",
			m.schemaConfig.TriggerName("circuit_breaker_state", "updated_at"),
			m.schemaConfig.TableCircuitBreakerState()),
	)

	// Drop functions
	statements = append(statements,
		fmt.Sprintf("DROP FUNCTION IF EXISTS %s() CASCADE", m.schemaConfig.FuncUpdateUpdatedAt()),
		fmt.Sprintf("DROP FUNCTION IF EXISTS %s() CASCADE", m.schemaConfig.FuncCleanupIdempotency()),
	)

	// Drop tables (in order respecting foreign keys)
	for _, table := range []string{
		m.schemaConfig.TableDeliveryAttempts(),
		m.schemaConfig.TableIdempotencyStore(),
		m.schemaConfig.TableDeliveries(),
		m.schemaConfig.TableCircuitBreakerState(),
		m.schemaConfig.TableSubscriptions(),
	} {
		statements = append(statements, fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", table))
	}

	return strings.Join(statements, ";\n") + ";"
}

// =============================================================================
// SCHEMA INFO
// =============================================================================

// SchemaInfo contains information about the current schema state.
type SchemaInfo struct {
	// Exists is true if schema tables exist for the configured prefix.
	Exists bool `json:"exists"`

	// Version is the schema version (empty if not exists).
	Version string `json:"version"`

	// Prefix is the configured table prefix.
	Prefix string `json:"prefix"`

	// TableNames lists all table names for this schema.
	TableNames []string `json:"table_names"`

	// LatestVersion is the version that would be installed by EnsureSchema.
	LatestVersion string `json:"latest_version"`

	// NeedsUpgrade is true if the schema exists but is older than LatestVersion.
	NeedsUpgrade bool `json:"needs_upgrade"`
}

// GetSchemaInfo returns detailed information about the current schema state.
func (m *SchemaManager) GetSchemaInfo(ctx context.Context) (*SchemaInfo, error) {
	exists, version, err := m.getSchemaVersion(ctx)
	if err != nil {
		return nil, err
	}

	return &SchemaInfo{
		Exists:        exists,
		Version:       version,
		Prefix:        m.schemaConfig.Prefix(),
		TableNames:    m.schemaConfig.AllTableNames(),
		LatestVersion: SchemaVersion,
		NeedsUpgrade:  exists && version != SchemaVersion,
	}, nil
}
