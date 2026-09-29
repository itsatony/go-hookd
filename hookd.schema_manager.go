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
	"github.com/lib/pq"
)

//go:embed schema.sql
var schemaTemplate string

// Template functions schema.sql spells every identifier with (go-hookd#9).
const (
	schemaTemplateFuncObject = "obj"
	schemaTemplateFuncIdent  = "ident"
)

// schemaVersionPattern extracts version from table comment.
// Expected format: "go-hookd schema v0.6.0 - ..."
var schemaVersionPattern = regexp.MustCompile(`go-hookd schema v(\d+\.\d+\.\d+)`)

// SchemaManager handles database schema setup for go-hookd.
// It creates or replaces tables with the configured prefix for multi-service isolation.
//
// Thread Safety: SchemaManager is safe for concurrent use. The underlying sql.DB
// handles connection pooling and concurrent access automatically.
//
// Cross-process safety (v0.9.0+): EnsureSchema and DropSchema serialize on a
// session-level PostgreSQL advisory lock keyed by the table prefix (see
// SchemaLockKey), so several processes booting together cannot race the
// DROP/CREATE batch. The lock is session-level: it requires a direct connection
// or a SESSION-mode pooler (not PgBouncer transaction pooling).
type SchemaManager struct {
	db           *sql.DB
	schemaConfig *SchemaConfig
	lockTimeout  time.Duration

	// optErr is the first option error from NewSchemaManager (which cannot
	// return one); it is returned by EnsureSchema and DropSchema.
	optErr error
}

// querier is satisfied by *sql.DB and *sql.Conn.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// applySchemaManagerOptions applies opts and returns the first error.
func (m *SchemaManager) applySchemaManagerOptions(opts []SchemaManagerOption) error {
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(m); err != nil {
			return err
		}
	}
	return nil
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
//
// The db pool is the caller's and is not reconfigured. Schema work uses ONE
// connection of it at a time. An invalid option is reported by EnsureSchema /
// DropSchema (this constructor cannot return an error).
func NewSchemaManager(db *sql.DB, schemaConfig *SchemaConfig, opts ...SchemaManagerOption) *SchemaManager {
	m := &SchemaManager{
		db:           db,
		schemaConfig: schemaConfig,
		lockTimeout:  DefaultSchemaLockTimeout,
	}
	m.optErr = m.applySchemaManagerOptions(opts)
	return m
}

// NewSchemaManagerFromURL creates a new SchemaManager by connecting to the given database URL.
//
// The schemaConfig determines the table prefix used for all database objects.
//
// Example:
//
//	schemaConfig, _ := hookd.NewSchemaConfig("myservice")
//	schemaMgr, err := hookd.NewSchemaManagerFromURL(dbURL, schemaConfig)
//
// Connection footprint (v0.9.0+): the pool this opens is bounded to
// SchemaManagerMaxOpenConns (1) open and SchemaManagerMaxIdleConns (0) idle, so
// a SchemaManager kept for the service lifetime holds NO connection between
// calls and never more than one. DB() returns this bounded pool; do not build a
// repository on it (use NewPostgresRepository). Close() it when done.
func NewSchemaManagerFromURL(dbURL string, schemaConfig *SchemaConfig, opts ...SchemaManagerOption) (*SchemaManager, error) {
	if schemaConfig == nil {
		return nil, cuserr.NewValidationError("schema_config", "schema configuration is required")
	}

	m := &SchemaManager{
		schemaConfig: schemaConfig,
		lockTimeout:  DefaultSchemaLockTimeout,
	}
	if err := m.applySchemaManagerOptions(opts); err != nil {
		return nil, err
	}

	db, err := sql.Open(PostgresDriverName, dbURL)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "open_connection"),
		)
	}

	// Boot-only helper: at most one connection, none retained while idle.
	db.SetMaxOpenConns(SchemaManagerMaxOpenConns)
	db.SetMaxIdleConns(SchemaManagerMaxIdleConns)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), PostgresPingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "ping"),
		)
	}

	m.db = db
	return m, nil
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
// Concurrency (v0.9.0+): safe to call from several processes at once. The
// create/replace path runs under the per-prefix advisory lock on a dedicated
// connection and RE-CHECKS the version after acquiring it, so exactly one
// caller creates the schema and the others find it current. Waiting is bounded
// by WithSchemaLockTimeout (default 60s) and ctx; a timeout is reported as a
// cuserr timeout error (errors.Is(err, cuserr.ErrTimeout)).
//
// Example:
//
//	if err := schemaMgr.EnsureSchema(ctx); err != nil {
//	    log.Fatal("schema setup failed:", err)
//	}
func (m *SchemaManager) EnsureSchema(ctx context.Context) error {
	if err := m.checkReady(); err != nil {
		return err
	}

	// Fast path (no lock): schema exists with current version.
	exists, currentVersion, err := m.getSchemaVersion(ctx, m.db)
	if err != nil {
		return err
	}
	if exists && currentVersion == SchemaVersion {
		return m.ensureAdditiveColumns(ctx)
	}

	// Process the template before taking the lock (pure, no I/O).
	schemaSQL, err := m.processTemplate()
	if err != nil {
		return err
	}

	return m.withSchemaLock(ctx, func(conn *sql.Conn) error {
		// Re-check under the lock: another process may have just created it.
		exists, currentVersion, err := m.getSchemaVersion(ctx, conn)
		if err != nil {
			return err
		}
		if exists && currentVersion == SchemaVersion {
			return nil
		}

		if _, err := conn.ExecContext(ctx, schemaDDLLockStatement()+schemaSQL); err != nil {
			return cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", opCreateSchema),
				cuserr.WithMetadata("prefix", m.schemaConfig.Prefix()),
			)
		}
		return nil
	})
}

// checkReady reports a missing connection or a deferred option error.
func (m *SchemaManager) checkReady() error {
	if m.db == nil {
		return cuserr.NewValidationError("database", "database connection is required")
	}
	if m.schemaConfig == nil {
		return cuserr.NewValidationError("schema_config", "schema configuration is required")
	}
	return m.optErr
}

// DropSchema drops all tables for the configured prefix.
// This is useful for testing and cleanup operations.
//
// WARNING: This is a destructive operation that permanently deletes all data.
//
// It runs under the same per-prefix advisory lock as EnsureSchema.
func (m *SchemaManager) DropSchema(ctx context.Context) error {
	if err := m.checkReady(); err != nil {
		return err
	}

	// Build DROP statements for all objects
	dropSQL := m.buildDropSQL()

	return m.withSchemaLock(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, schemaDDLLockStatement()+dropSQL); err != nil {
			return cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", opDropSchema),
				cuserr.WithMetadata("prefix", m.schemaConfig.Prefix()),
			)
		}
		return nil
	})
}

// SchemaExists checks if schema tables exist for the configured prefix.
func (m *SchemaManager) SchemaExists(ctx context.Context) (bool, error) {
	exists, _, err := m.getSchemaVersion(ctx, m.db)
	return exists, err
}

// GetSchemaVersion returns the current schema version if tables exist.
// Returns empty string if tables don't exist.
func (m *SchemaManager) GetSchemaVersion(ctx context.Context) (string, error) {
	exists, version, err := m.getSchemaVersion(ctx, m.db)
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
// q is the connection to ask: m.db, or the dedicated lock connection.
func (m *SchemaManager) getSchemaVersion(ctx context.Context, q querier) (exists bool, version string, err error) {
	if m.db == nil || q == nil {
		return false, "", cuserr.NewValidationError("database", "database connection is required")
	}

	// Check if the subscriptions table exists (primary table)
	tableName := m.schemaConfig.TableSubscriptions()

	// ⛔ The table is looked up with to_regclass, i.e. through search_path —
	// EXACTLY how schema.sql's unqualified DROP/CREATE resolve it. Any other
	// lookup ('public', current_schema()) can disagree with the DDL, and then
	// "not found" here is followed by a DROP that finds (and empties) a table
	// elsewhere on the search_path.
	query := `
		SELECT c.oid IS NOT NULL, obj_description(c.oid, 'pg_class')
		FROM (SELECT to_regclass($1)::oid AS oid) c
	`

	var found bool
	var comment sql.NullString
	err = q.QueryRowContext(ctx, query, tableName).Scan(&found, &comment)
	if err == nil && !found {
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
	tmpl, err := template.New("schema").Funcs(template.FuncMap{
		// {prefix}_hookd_{name}: tables and functions.
		schemaTemplateFuncObject: m.schemaConfig.tableName,
		// {kind}_{prefix}_hookd_{rest}: indexes, constraints, triggers.
		schemaTemplateFuncIdent: m.schemaConfig.DerivedName,
	}).Parse(schemaTemplate)
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
			m.schemaConfig.TriggerName("circuit_breaker", "updated_at"),
			m.schemaConfig.TableCircuitBreakerState()),
	)

	// Drop functions. A function name over 63 bytes is shortened since
	// v0.11.4; a schema created before that holds the SERVER-truncated name
	// instead (prefixes of 26-30 chars applied fine, silently truncated), so
	// that legacy spelling is dropped too rather than left orphaned.
	for _, fn := range m.schemaConfig.allFunctionNamesWithLegacy() {
		statements = append(statements, fmt.Sprintf("DROP FUNCTION IF EXISTS %s() CASCADE", fn))
	}

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
	exists, version, err := m.getSchemaVersion(ctx, m.db)
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

// additiveColumn is a NULLABLE column added to the baseline schema after its
// SchemaVersion was fixed. It is applied in place — never by bumping
// SchemaVersion, because a version change makes EnsureSchema DROP and recreate
// every table, and an OLDER binary booting during a rolling deploy would read a
// newer version comment as "different" and drop the upgraded schema. A nullable
// column is invisible to older binaries (every query names its columns).
type additiveColumn struct {
	tableSuffix string
	column      string
	sqlType     string
}

// schemaAdditiveColumns are applied by EnsureSchema to an existing
// SchemaVersion schema that lacks them. Fresh schemas get them from schema.sql.
var schemaAdditiveColumns = []additiveColumn{
	// v0.11.0 (go-hookd#2): the queuer's key, sent as X-Webhook-Idempotency-Key.
	{tableSuffix: TableSuffixDeliveries, column: "idempotency_key", sqlType: "VARCHAR(255)"},
	// v0.11.1: the original retry budget, renewed (not compounded) by a redrive.
	{tableSuffix: TableSuffixDeliveries, column: "attempt_budget", sqlType: "INTEGER"},
}

// Operation names and statements for additive schema changes.
const (
	sqlSetAdditiveLockTimeout  = "SET lock_timeout = '5s'"
	sqlResetLockTimeout        = "RESET lock_timeout"
	opCheckAdditiveColumns     = "check_additive_columns"
	opAddAdditiveColumn        = "add_additive_column"
	opDropObsoleteConstraint   = "drop_obsolete_constraint"
	opCheckObsoleteConstraints = "check_obsolete_constraints"
)

// missingAdditiveColumns lists the additive columns the schema lacks.
func (m *SchemaManager) missingAdditiveColumns(ctx context.Context, q querier) ([]additiveColumn, error) {
	var missing []additiveColumn
	for _, col := range schemaAdditiveColumns {
		var present bool
		// Resolved through search_path, like the ALTER that follows (see
		// getSchemaVersion).
		err := q.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_attribute
				WHERE attrelid = to_regclass($1) AND attname = $2 AND NOT attisdropped
			)`, m.schemaConfig.tableName(col.tableSuffix), col.column).Scan(&present)
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", opCheckAdditiveColumns),
			)
		}
		if !present {
			missing = append(missing, col)
		}
	}
	return missing, nil
}

// ensureAdditiveColumns adds any missing additive column. The check is lock
// free (the common case: nothing to do); the ALTER runs under the per-prefix
// schema lock and re-checks, so concurrent boots add each column once.
func (m *SchemaManager) ensureAdditiveColumns(ctx context.Context) error {
	missing, err := m.missingAdditiveColumns(ctx, m.db)
	if err != nil {
		return err
	}
	obsolete, err := m.presentObsoleteConstraints(ctx, m.db)
	if err != nil || (len(missing) == 0 && len(obsolete) == 0) {
		return err
	}
	return m.withSchemaLock(ctx, func(conn *sql.Conn) error {
		missing, err := m.missingAdditiveColumns(ctx, conn)
		if err != nil {
			return err
		}
		obsolete, err := m.presentObsoleteConstraints(ctx, conn)
		if err != nil {
			return err
		}
		// ADD COLUMN takes ACCESS EXCLUSIVE; bounded so a boot behind a long
		// transaction fails fast (and retries next boot) instead of queueing
		// every other query on the table behind it.
		if _, err := conn.ExecContext(ctx, sqlSetAdditiveLockTimeout); err != nil {
			return cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", opAddAdditiveColumn),
			)
		}
		defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), sqlResetLockTimeout) }()
		for _, col := range missing {
			stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s`,
				m.schemaConfig.tableName(col.tableSuffix), col.column, col.sqlType)
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return cuserr.NewExternalError("database", "postgres", err,
					cuserr.WithMetadata("operation", opAddAdditiveColumn),
					cuserr.WithMetadata("column", col.column),
				)
			}
		}
		for _, c := range obsolete {
			stmt := fmt.Sprintf(`ALTER TABLE %s DROP CONSTRAINT IF EXISTS %s`,
				c.table, pq.QuoteIdentifier(c.name))
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return cuserr.NewExternalError("database", "postgres", err,
					cuserr.WithMetadata("operation", opDropObsoleteConstraint),
				)
			}
		}
		return nil
	})
}

// obsoleteForeignKey is a baseline foreign key later releases removed IN PLACE
// (same no-version-bump rule as additiveColumn: dropping a constraint is
// invisible to older binaries). It is identified by STRUCTURE — a foreign key
// from tableSuffix to refTableSuffix — never by name: PostgreSQL truncates
// identifiers to 63 bytes, so for long prefixes the stored name differs from
// the one schema.sql spelled.
type obsoleteForeignKey struct {
	tableSuffix    string
	refTableSuffix string
}

// schemaObsoleteForeignKeys are dropped from existing schemas by EnsureSchema.
var schemaObsoleteForeignKeys = []obsoleteForeignKey{
	// v0.11.1: an inline delivery's idempotency scope is not a subscription
	// id, so this FK made every keyed QueueInlineDelivery fail.
	{tableSuffix: TableSuffixIdempotencyStore, refTableSuffix: TableSuffixSubscriptions},
}

// presentObsoleteConstraint is one constraint to drop.
type presentObsoleteConstraint struct {
	table string
	name  string
}

// presentObsoleteConstraints lists the obsolete foreign keys the schema still
// has, by their stored names (tables resolved via to_regclass, like the DDL).
func (m *SchemaManager) presentObsoleteConstraints(ctx context.Context, q querier) ([]presentObsoleteConstraint, error) {
	rq, ok := q.(interface {
		QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	})
	if !ok {
		return nil, cuserr.NewInternalError("schema_manager", nil,
			cuserr.WithMetadata("operation", opCheckObsoleteConstraints))
	}
	var present []presentObsoleteConstraint
	for _, fk := range schemaObsoleteForeignKeys {
		table := m.schemaConfig.tableName(fk.tableSuffix)
		rows, err := rq.QueryContext(ctx, `
			SELECT conname FROM pg_constraint
			WHERE contype = 'f' AND conrelid = to_regclass($1) AND confrelid = to_regclass($2)`,
			table, m.schemaConfig.tableName(fk.refTableSuffix))
		if err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", opCheckObsoleteConstraints))
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return nil, cuserr.NewExternalError("database", "postgres", err,
					cuserr.WithMetadata("operation", opCheckObsoleteConstraints))
			}
			present = append(present, presentObsoleteConstraint{table: table, name: name})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", opCheckObsoleteConstraints))
		}
		_ = rows.Close()
	}
	return present, nil
}
