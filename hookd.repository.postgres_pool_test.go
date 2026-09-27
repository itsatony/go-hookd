package hookd

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyRepoOptions runs options against a fresh repository the way
// NewPostgresRepository does, without opening a connection.
func applyRepoOptions(t *testing.T, opts ...PostgresRepositoryOption) (postgresPoolConfig, error) {
	t.Helper()
	r := &PostgresRepository{pool: defaultPostgresPoolConfig()}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return postgresPoolConfig{}, err
		}
	}
	return r.pool.resolve()
}

func TestPostgresPoolConfig_DefaultsArePreV090Values(t *testing.T) {
	cfg, err := applyRepoOptions(t)
	require.NoError(t, err)
	assert.Equal(t, 25, cfg.maxOpenConns)
	assert.Equal(t, 5, cfg.maxIdleConns)
	assert.Equal(t, 5*time.Minute, cfg.connMaxLifetime)
	assert.Equal(t, 1*time.Minute, cfg.connMaxIdleTime)
}

func TestPostgresPoolConfig_Overrides(t *testing.T) {
	cfg, err := applyRepoOptions(t,
		WithMaxOpenConns(4),
		WithMaxIdleConns(2),
		WithConnMaxLifetime(30*time.Second),
		WithConnMaxIdleTime(10*time.Second),
	)
	require.NoError(t, err)
	assert.Equal(t, 4, cfg.maxOpenConns)
	assert.Equal(t, 2, cfg.maxIdleConns)
	assert.Equal(t, 30*time.Second, cfg.connMaxLifetime)
	assert.Equal(t, 10*time.Second, cfg.connMaxIdleTime)
}

func TestPostgresPoolConfig_DefaultIdleIsClampedToOpen(t *testing.T) {
	cfg, err := applyRepoOptions(t, WithMaxOpenConns(3))
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.maxOpenConns)
	assert.Equal(t, 3, cfg.maxIdleConns, "default idle (5) must clamp down to max open (3)")
}

func TestPostgresPoolConfig_ExplicitIdleAboveOpenIsRefused(t *testing.T) {
	// Order must not matter: the check runs after every option.
	for _, opts := range [][]PostgresRepositoryOption{
		{WithMaxOpenConns(3), WithMaxIdleConns(4)},
		{WithMaxIdleConns(4), WithMaxOpenConns(3)},
	} {
		_, err := applyRepoOptions(t, opts...)
		require.Error(t, err)
		assert.Contains(t, err.Error(), ErrMsgMaxIdleExceedsOpen)
	}
}

func TestPostgresPoolConfig_IdleZeroAndEqualAreAccepted(t *testing.T) {
	cfg, err := applyRepoOptions(t, WithMaxOpenConns(2), WithMaxIdleConns(0))
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.maxIdleConns)

	cfg, err = applyRepoOptions(t, WithMaxOpenConns(2), WithMaxIdleConns(2))
	require.NoError(t, err)
	assert.Equal(t, 2, cfg.maxIdleConns)
}

func TestPostgresPoolConfig_InvalidValues(t *testing.T) {
	cases := []struct {
		name string
		opt  PostgresRepositoryOption
		msg  string
	}{
		{"open zero", WithMaxOpenConns(0), ErrMsgMaxOpenConnsInvalid},
		{"open negative", WithMaxOpenConns(-1), ErrMsgMaxOpenConnsInvalid},
		{"idle negative", WithMaxIdleConns(-1), ErrMsgMaxIdleConnsInvalid},
		{"lifetime zero", WithConnMaxLifetime(0), ErrMsgConnMaxLifetimeInvalid},
		{"lifetime negative", WithConnMaxLifetime(-time.Second), ErrMsgConnMaxLifetimeInvalid},
		{"idle time zero", WithConnMaxIdleTime(0), ErrMsgConnMaxIdleTimeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyRepoOptions(t, tc.opt)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.msg)
		})
	}
}

// TestNewPostgresRepository_RefusesBadPoolBeforeConnecting proves validation
// runs before any connection attempt (the DSN is unreachable on purpose).
func TestNewPostgresRepository_RefusesBadPoolBeforeConnecting(t *testing.T) {
	_, err := NewPostgresRepository("postgres://u:p@127.0.0.1:1/db?sslmode=disable",
		WithTablePrefix("pooltest"), WithMaxOpenConns(1), WithMaxIdleConns(2))
	require.Error(t, err)
	assert.Contains(t, err.Error(), ErrMsgMaxIdleExceedsOpen)
}

func TestPostgresPoolConfig_ApplySetsDatabaseSQLPool(t *testing.T) {
	db, err := sql.Open(PostgresDriverName, "postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cfg, err := applyRepoOptions(t, WithMaxOpenConns(7))
	require.NoError(t, err)
	cfg.apply(db)
	assert.Equal(t, 7, db.Stats().MaxOpenConnections)
}

func TestPostgresRepository_PoolStatsWithoutDB(t *testing.T) {
	var r PostgresRepository
	assert.Equal(t, sql.DBStats{}, r.PoolStats())
}
