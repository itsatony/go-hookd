package hookd

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaLockKey_StableAndPrefixScoped(t *testing.T) {
	c1, k1 := SchemaLockKey("deepr")
	c2, k2 := SchemaLockKey("deepr")
	c3, k3 := SchemaLockKey("trove")

	assert.Equal(t, SchemaLockClassID, c1)
	assert.Equal(t, c1, c2)
	assert.Equal(t, c1, c3)
	assert.Equal(t, k1, k2, "same prefix must yield the same key")
	assert.NotEqual(t, k1, k3, "different prefixes must not share a lock")
	assert.NotEqual(t, SchemaLockClassID, SchemaDDLLockClassID, "per-prefix and DDL locks must be distinct classes")
}

func TestSchemaDDLLockStatement(t *testing.T) {
	stmt := schemaDDLLockStatement()
	assert.True(t, strings.HasPrefix(stmt, "SELECT pg_advisory_xact_lock("))
	assert.True(t, strings.HasSuffix(stmt, ";\n"))
}

func TestWithSchemaLockTimeout(t *testing.T) {
	cfg, err := NewSchemaConfig("locktest")
	require.NoError(t, err)

	m := NewSchemaManager(nil, cfg)
	assert.Equal(t, DefaultSchemaLockTimeout, m.lockTimeout)

	m = NewSchemaManager(nil, cfg, WithSchemaLockTimeout(3*time.Second))
	require.NoError(t, m.optErr)
	assert.Equal(t, 3*time.Second, m.lockTimeout)

	m = NewSchemaManager(nil, cfg, nil) // nil option is ignored
	require.NoError(t, m.optErr)
}

func TestWithSchemaLockTimeout_InvalidIsReportedByEnsureSchema(t *testing.T) {
	cfg, err := NewSchemaConfig("locktest")
	require.NoError(t, err)

	// A non-nil (never-connected) pool so the option error, not the nil-db
	// check, is what EnsureSchema reports.
	db, err := sql.Open(PostgresDriverName, "postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	m := NewSchemaManager(db, cfg, WithSchemaLockTimeout(0))
	err = m.EnsureSchema(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrMsgSchemaLockTimeoutInvalid)

	err = m.DropSchema(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrMsgSchemaLockTimeoutInvalid)
}

func TestNewSchemaManagerFromURL_RefusesBadOptionBeforeConnecting(t *testing.T) {
	cfg, err := NewSchemaConfig("locktest")
	require.NoError(t, err)
	_, err = NewSchemaManagerFromURL("postgres://u:p@127.0.0.1:1/db?sslmode=disable", cfg,
		WithSchemaLockTimeout(time.Microsecond))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrMsgSchemaLockTimeoutInvalid)
}
