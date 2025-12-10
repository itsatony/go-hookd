// Package hookd provides webhook management functionality.
//
// This file implements programmatic database migration support using embedded SQL files.
// Users can run migrations programmatically or use the embedded SQL files with their
// preferred migration tool.
package hookd

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsatony/go-cuserr"
)

//go:embed migrations/postgres/*.sql
var migrationsFS embed.FS

// MigrateDirection indicates the direction of migration.
type MigrateDirection string

const (
	// MigrateUp applies pending migrations.
	MigrateUp MigrateDirection = "up"

	// MigrateDown rolls back migrations.
	MigrateDown MigrateDirection = "down"
)

// MigrationStatus represents the current migration state.
type MigrationStatus struct {
	// CurrentVersion is the currently applied migration version (0 if none).
	CurrentVersion int `json:"current_version"`

	// LatestVersion is the highest available migration version.
	LatestVersion int `json:"latest_version"`

	// AppliedMigrations is the list of applied migrations.
	AppliedMigrations []AppliedMigration `json:"applied_migrations"`

	// PendingMigrations is the list of migrations not yet applied.
	PendingMigrations []string `json:"pending_migrations"`

	// IsCurrent is true if all migrations have been applied.
	IsCurrent bool `json:"is_current"`
}

// AppliedMigration represents a migration that has been applied.
type AppliedMigration struct {
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	AppliedAt time.Time `json:"applied_at"`
}

// MigrationFile represents an embedded migration file.
type MigrationFile struct {
	Version   int
	Name      string
	Direction MigrateDirection
	Content   string
}

// Migrator provides database migration functionality.
type Migrator struct {
	db *sql.DB
}

// NewMigrator creates a new Migrator with the given database connection.
func NewMigrator(db *sql.DB) *Migrator {
	return &Migrator{db: db}
}

// NewMigratorFromURL creates a new Migrator by connecting to the given database URL.
func NewMigratorFromURL(dbURL string) (*Migrator, error) {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "open_connection"),
		)
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "ping"),
		)
	}

	return &Migrator{db: db}, nil
}

// Close closes the database connection.
func (m *Migrator) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

// Migrate runs migrations in the specified direction.
//
// For MigrateUp: applies all pending migrations in order.
// For MigrateDown: rolls back the most recently applied migration.
//
// Returns the number of migrations applied/rolled back.
func (m *Migrator) Migrate(ctx context.Context, direction MigrateDirection) (int, error) {
	// Ensure migration tracking table exists
	if err := m.ensureMigrationTable(ctx); err != nil {
		return 0, err
	}

	migrations, err := m.loadMigrations(direction)
	if err != nil {
		return 0, err
	}

	applied := 0

	switch direction {
	case MigrateUp:
		appliedVersions, err := m.getAppliedVersions(ctx)
		if err != nil {
			return 0, err
		}

		for _, mig := range migrations {
			if appliedVersions[mig.Version] {
				continue // Already applied
			}

			if err := m.applyMigration(ctx, mig); err != nil {
				return applied, fmt.Errorf("failed to apply migration %d (%s): %w",
					mig.Version, mig.Name, err)
			}
			applied++
		}

	case MigrateDown:
		// Get the most recently applied migration
		currentVersion, err := m.getCurrentVersion(ctx)
		if err != nil {
			return 0, err
		}

		if currentVersion == 0 {
			return 0, nil // Nothing to roll back
		}

		// Find the down migration for the current version
		var downMigration *MigrationFile
		for i := range migrations {
			if migrations[i].Version == currentVersion {
				downMigration = &migrations[i]
				break
			}
		}

		if downMigration == nil {
			return 0, cuserr.NewNotFoundError("migration", fmt.Sprintf("%d", currentVersion))
		}

		if err := m.rollbackMigration(ctx, *downMigration); err != nil {
			return 0, fmt.Errorf("failed to rollback migration %d (%s): %w",
				downMigration.Version, downMigration.Name, err)
		}
		applied = 1
	}

	return applied, nil
}

// MigrateToVersion migrates to a specific version.
//
// If the target version is higher than current, applies up migrations.
// If the target version is lower than current, applies down migrations.
func (m *Migrator) MigrateToVersion(ctx context.Context, targetVersion int) (int, error) {
	if err := m.ensureMigrationTable(ctx); err != nil {
		return 0, err
	}

	currentVersion, err := m.getCurrentVersion(ctx)
	if err != nil {
		return 0, err
	}

	if currentVersion == targetVersion {
		return 0, nil // Already at target version
	}

	applied := 0

	if targetVersion > currentVersion {
		// Apply up migrations
		migrations, err := m.loadMigrations(MigrateUp)
		if err != nil {
			return 0, err
		}

		for _, mig := range migrations {
			if mig.Version <= currentVersion || mig.Version > targetVersion {
				continue
			}

			if err := m.applyMigration(ctx, mig); err != nil {
				return applied, fmt.Errorf("failed to apply migration %d (%s): %w",
					mig.Version, mig.Name, err)
			}
			applied++
		}
	} else {
		// Apply down migrations
		migrations, err := m.loadMigrations(MigrateDown)
		if err != nil {
			return 0, err
		}

		// Sort descending for rollback
		sort.Slice(migrations, func(i, j int) bool {
			return migrations[i].Version > migrations[j].Version
		})

		for _, mig := range migrations {
			if mig.Version <= targetVersion || mig.Version > currentVersion {
				continue
			}

			if err := m.rollbackMigration(ctx, mig); err != nil {
				return applied, fmt.Errorf("failed to rollback migration %d (%s): %w",
					mig.Version, mig.Name, err)
			}
			applied++
		}
	}

	return applied, nil
}

// Status returns the current migration status.
func (m *Migrator) Status(ctx context.Context) (*MigrationStatus, error) {
	if err := m.ensureMigrationTable(ctx); err != nil {
		return nil, err
	}

	// Get applied migrations
	rows, err := m.db.QueryContext(ctx, `
		SELECT version, name, applied_at
		FROM schema_migrations
		ORDER BY version ASC
	`)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "query_migrations"),
		)
	}
	defer rows.Close()

	appliedVersions := make(map[int]bool)
	var appliedMigrations []AppliedMigration

	for rows.Next() {
		var am AppliedMigration
		if err := rows.Scan(&am.Version, &am.Name, &am.AppliedAt); err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_migration"),
			)
		}
		appliedMigrations = append(appliedMigrations, am)
		appliedVersions[am.Version] = true
	}

	if err := rows.Err(); err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "iterate_migrations"),
		)
	}

	// Get available migrations
	migrations, err := m.loadMigrations(MigrateUp)
	if err != nil {
		return nil, err
	}

	var pendingMigrations []string
	latestVersion := 0

	for _, mig := range migrations {
		if mig.Version > latestVersion {
			latestVersion = mig.Version
		}
		if !appliedVersions[mig.Version] {
			pendingMigrations = append(pendingMigrations, fmt.Sprintf("%06d_%s", mig.Version, mig.Name))
		}
	}

	currentVersion := 0
	if len(appliedMigrations) > 0 {
		currentVersion = appliedMigrations[len(appliedMigrations)-1].Version
	}

	return &MigrationStatus{
		CurrentVersion:    currentVersion,
		LatestVersion:     latestVersion,
		AppliedMigrations: appliedMigrations,
		PendingMigrations: pendingMigrations,
		IsCurrent:         len(pendingMigrations) == 0,
	}, nil
}

// GetMigrationFiles returns all embedded migration files.
// This is useful for users who want to use their own migration tool.
func GetMigrationFiles() ([]MigrationFile, error) {
	var files []MigrationFile

	entries, err := fs.ReadDir(migrationsFS, "migrations/postgres")
	if err != nil {
		return nil, cuserr.NewInternalError("migrations", err,
			cuserr.WithMetadata("operation", "read_dir"),
		)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		mig, err := parseMigrationFile(entry.Name())
		if err != nil {
			continue // Skip invalid files
		}

		content, err := fs.ReadFile(migrationsFS, filepath.Join("migrations/postgres", entry.Name()))
		if err != nil {
			return nil, cuserr.NewInternalError("migrations", err,
				cuserr.WithMetadata("operation", "read_file"),
				cuserr.WithMetadata("file", entry.Name()),
			)
		}

		mig.Content = string(content)
		files = append(files, mig)
	}

	// Sort by version
	sort.Slice(files, func(i, j int) bool {
		return files[i].Version < files[j].Version
	})

	return files, nil
}

// GetMigrationSQL returns the SQL content for a specific migration.
func GetMigrationSQL(version int, direction MigrateDirection) (string, error) {
	files, err := GetMigrationFiles()
	if err != nil {
		return "", err
	}

	for _, f := range files {
		if f.Version == version && f.Direction == direction {
			return f.Content, nil
		}
	}

	return "", cuserr.NewNotFoundError("migration",
		fmt.Sprintf("%d_%s", version, direction))
}

// =============================================================================
// INTERNAL HELPERS
// =============================================================================

func (m *Migrator) ensureMigrationTable(ctx context.Context) error {
	_, err := m.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			applied_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "create_migration_table"),
		)
	}
	return nil
}

func (m *Migrator) loadMigrations(direction MigrateDirection) ([]MigrationFile, error) {
	files, err := GetMigrationFiles()
	if err != nil {
		return nil, err
	}

	var result []MigrationFile
	for _, f := range files {
		if f.Direction == direction {
			result = append(result, f)
		}
	}

	// Sort ascending for up, descending for down
	if direction == MigrateUp {
		sort.Slice(result, func(i, j int) bool {
			return result[i].Version < result[j].Version
		})
	} else {
		sort.Slice(result, func(i, j int) bool {
			return result[i].Version > result[j].Version
		})
	}

	return result, nil
}

func (m *Migrator) getAppliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "query_applied_versions"),
		)
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, cuserr.NewExternalError("database", "postgres", err,
				cuserr.WithMetadata("operation", "scan_version"),
			)
		}
		applied[version] = true
	}

	return applied, rows.Err()
}

func (m *Migrator) getCurrentVersion(ctx context.Context) (int, error) {
	var version int
	err := m.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM schema_migrations
	`).Scan(&version)

	if err != nil {
		return 0, cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "get_current_version"),
		)
	}

	return version, nil
}

func (m *Migrator) applyMigration(ctx context.Context, mig MigrationFile) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "begin_tx"),
		)
	}
	defer tx.Rollback()

	// Execute migration SQL
	if _, err := tx.ExecContext(ctx, mig.Content); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "execute_migration"),
			cuserr.WithMetadata("version", strconv.Itoa(mig.Version)),
		)
	}

	// Record migration
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, name, applied_at)
		VALUES ($1, $2, NOW())
	`, mig.Version, mig.Name); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "record_migration"),
			cuserr.WithMetadata("version", strconv.Itoa(mig.Version)),
		)
	}

	return tx.Commit()
}

func (m *Migrator) rollbackMigration(ctx context.Context, mig MigrationFile) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "begin_tx"),
		)
	}
	defer tx.Rollback()

	// Execute rollback SQL
	if _, err := tx.ExecContext(ctx, mig.Content); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "execute_rollback"),
			cuserr.WithMetadata("version", strconv.Itoa(mig.Version)),
		)
	}

	// Remove migration record
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM schema_migrations WHERE version = $1
	`, mig.Version); err != nil {
		return cuserr.NewExternalError("database", "postgres", err,
			cuserr.WithMetadata("operation", "remove_migration_record"),
			cuserr.WithMetadata("version", strconv.Itoa(mig.Version)),
		)
	}

	return tx.Commit()
}

func parseMigrationFile(filename string) (MigrationFile, error) {
	// Expected format: 000001_name.up.sql or 000001_name.down.sql
	base := strings.TrimSuffix(filename, ".sql")

	var direction MigrateDirection
	if strings.HasSuffix(base, ".up") {
		direction = MigrateUp
		base = strings.TrimSuffix(base, ".up")
	} else if strings.HasSuffix(base, ".down") {
		direction = MigrateDown
		base = strings.TrimSuffix(base, ".down")
	} else {
		return MigrationFile{}, fmt.Errorf("invalid migration file format: %s", filename)
	}

	// Parse version and name
	parts := strings.SplitN(base, "_", 2)
	if len(parts) != 2 {
		return MigrationFile{}, fmt.Errorf("invalid migration file format: %s", filename)
	}

	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return MigrationFile{}, fmt.Errorf("invalid version number in %s: %w", filename, err)
	}

	return MigrationFile{
		Version:   version,
		Name:      parts[1],
		Direction: direction,
	}, nil
}
