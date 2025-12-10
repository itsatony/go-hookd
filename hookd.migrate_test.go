package hookd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMigrationFiles(t *testing.T) {
	files, err := GetMigrationFiles()
	require.NoError(t, err)
	require.NotEmpty(t, files, "should have embedded migration files")

	// Verify we have both up and down migrations
	var upCount, downCount int
	for _, f := range files {
		switch f.Direction {
		case MigrateUp:
			upCount++
		case MigrateDown:
			downCount++
		}
	}

	assert.Greater(t, upCount, 0, "should have up migrations")
	assert.Greater(t, downCount, 0, "should have down migrations")
	assert.Equal(t, upCount, downCount, "should have matching up/down migrations")

	// Verify first migration content exists
	var hasVersion1Up, hasVersion1Down bool
	for _, f := range files {
		if f.Version == 1 {
			assert.NotEmpty(t, f.Content, "migration content should not be empty")
			assert.NotEmpty(t, f.Name, "migration name should not be empty")
			if f.Direction == MigrateUp {
				hasVersion1Up = true
				assert.Contains(t, f.Content, "CREATE TABLE", "up migration should create tables")
			}
			if f.Direction == MigrateDown {
				hasVersion1Down = true
				assert.Contains(t, f.Content, "DROP", "down migration should drop tables")
			}
		}
	}

	assert.True(t, hasVersion1Up, "should have version 1 up migration")
	assert.True(t, hasVersion1Down, "should have version 1 down migration")
}

func TestGetMigrationSQL(t *testing.T) {
	t.Run("existing up migration", func(t *testing.T) {
		sql, err := GetMigrationSQL(1, MigrateUp)
		require.NoError(t, err)
		assert.NotEmpty(t, sql)
		assert.Contains(t, sql, "CREATE TABLE")
	})

	t.Run("existing down migration", func(t *testing.T) {
		sql, err := GetMigrationSQL(1, MigrateDown)
		require.NoError(t, err)
		assert.NotEmpty(t, sql)
		assert.Contains(t, sql, "DROP")
	})

	t.Run("non-existent migration", func(t *testing.T) {
		_, err := GetMigrationSQL(999, MigrateUp)
		assert.Error(t, err)
	})
}

func TestParseMigrationFile(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		wantVer   int
		wantName  string
		wantDir   MigrateDirection
		wantError bool
	}{
		{
			name:     "valid up migration",
			filename: "000001_create_tables.up.sql",
			wantVer:  1,
			wantName: "create_tables",
			wantDir:  MigrateUp,
		},
		{
			name:     "valid down migration",
			filename: "000001_create_tables.down.sql",
			wantVer:  1,
			wantName: "create_tables",
			wantDir:  MigrateDown,
		},
		{
			name:     "higher version",
			filename: "000123_add_columns.up.sql",
			wantVer:  123,
			wantName: "add_columns",
			wantDir:  MigrateUp,
		},
		{
			name:      "missing direction",
			filename:  "000001_create_tables.sql",
			wantError: true,
		},
		{
			name:      "missing version",
			filename:  "create_tables.up.sql",
			wantError: true,
		},
		{
			name:      "invalid version",
			filename:  "abcdef_create_tables.up.sql",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mig, err := parseMigrationFile(tt.filename)
			if tt.wantError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantVer, mig.Version)
			assert.Equal(t, tt.wantName, mig.Name)
			assert.Equal(t, tt.wantDir, mig.Direction)
		})
	}
}

func TestNewMigrator(t *testing.T) {
	// Test with nil db (should work - db operations will fail at runtime)
	m := NewMigrator(nil)
	assert.NotNil(t, m)
}

func TestMigrationTypes(t *testing.T) {
	// Test that constants are correct
	assert.Equal(t, MigrateDirection("up"), MigrateUp)
	assert.Equal(t, MigrateDirection("down"), MigrateDown)
}

func TestMigrationStatus_Struct(t *testing.T) {
	// Test struct initialization
	status := MigrationStatus{
		CurrentVersion:    1,
		LatestVersion:     2,
		IsCurrent:         false,
		PendingMigrations: []string{"000002_add_filters"},
		AppliedMigrations: []AppliedMigration{
			{Version: 1, Name: "create_tables"},
		},
	}

	assert.Equal(t, 1, status.CurrentVersion)
	assert.Equal(t, 2, status.LatestVersion)
	assert.False(t, status.IsCurrent)
	assert.Len(t, status.PendingMigrations, 1)
	assert.Len(t, status.AppliedMigrations, 1)
}

func TestMigrationFile_Struct(t *testing.T) {
	mf := MigrationFile{
		Version:   1,
		Name:      "create_tables",
		Direction: MigrateUp,
		Content:   "CREATE TABLE test;",
	}

	assert.Equal(t, 1, mf.Version)
	assert.Equal(t, "create_tables", mf.Name)
	assert.Equal(t, MigrateUp, mf.Direction)
	assert.Contains(t, mf.Content, "CREATE TABLE")
}
