//go:build integration

// Package integration_test holds real-PostgreSQL integration tests that live in
// their own package so they compile independently of the root package's
// integration-tagged files.
//
// Run: go test -race -tags=integration ./integration/
package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/itsatony/go-cuserr"
	hookd "github.com/itsatony/go-hookd"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	postgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	testPostgresImage   = "postgres:16-alpine"
	testDatabase        = "hookd_lock_test"
	testUser            = "test"
	testPassword        = "test"
	concurrentBooters   = 3
	concurrentRounds    = 5
	testOpTimeout       = 90 * time.Second
	shortLockTimeout    = 500 * time.Millisecond
	otherPrefixDeadline = 10 * time.Second
)

// startPostgres starts a fresh PostgreSQL container and returns its DSN.
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	pg, err := postgres.Run(ctx, testPostgresImage,
		postgres.WithDatabase(testDatabase),
		postgres.WithUsername(testUser),
		postgres.WithPassword(testPassword),
		postgres.BasicWaitStrategies(),
		postgres.WithSQLDriver("postgres"),
	)
	require.NoError(t, err, "start postgres container")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pg.Terminate(ctx)
	})
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	return dsn
}

func openDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())
	return db
}

// ensureConcurrently runs EnsureSchema for prefix from n independent
// SchemaManagers (each with its OWN pool, like n booting processes), released
// together by a barrier. It returns every error.
func ensureConcurrently(t *testing.T, dsn, prefix string, n int) []error {
	t.Helper()
	cfg, err := hookd.NewSchemaConfig(prefix)
	require.NoError(t, err)

	managers := make([]*hookd.SchemaManager, n)
	for i := range managers {
		managers[i], err = hookd.NewSchemaManagerFromURL(dsn, cfg)
		require.NoError(t, err)
	}
	defer func() {
		for _, m := range managers {
			_ = m.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), testOpTimeout)
	defer cancel()

	start := make(chan struct{})
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i, m := range managers {
		wg.Add(1)
		go func(i int, m *hookd.SchemaManager) {
			defer wg.Done()
			<-start
			errs[i] = m.EnsureSchema(ctx)
		}(i, m)
	}
	close(start)
	wg.Wait()
	return errs
}

// assertSchemaComplete checks every table, function and trigger of prefix and
// the version comment.
func assertSchemaComplete(t *testing.T, db *sql.DB, prefix string) {
	t.Helper()
	ctx := context.Background()
	cfg, err := hookd.NewSchemaConfig(prefix)
	require.NoError(t, err)

	for _, table := range cfg.AllTableNames() {
		var exists bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`,
			table).Scan(&exists))
		assert.True(t, exists, "table %s must exist", table)
	}

	var funcs int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_proc WHERE proname IN ($1, $2)`,
		cfg.FuncUpdateUpdatedAt(), cfg.FuncCleanupIdempotency()).Scan(&funcs))
	assert.Equal(t, 2, funcs, "both functions must exist exactly once")

	var triggers int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname LIKE $1`,
		"trg_"+prefix+"_hookd_%").Scan(&triggers))
	assert.Equal(t, 2, triggers, "both triggers must exist exactly once")

	info, err := hookd.NewSchemaManager(db, cfg).GetSchemaInfo(ctx)
	require.NoError(t, err)
	assert.True(t, info.Exists)
	assert.Equal(t, hookd.SchemaVersion, info.Version)
	assert.False(t, info.NeedsUpgrade)
}

// heldSchemaLocks counts go-hookd schema advisory locks currently held.
func heldSchemaLocks(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND granted AND classid::bigint = $1::bigint`,
		int64(hookd.SchemaLockClassID)).Scan(&n))
	return n
}

// installCreateTableCounter installs an event trigger recording every
// COMMITTED `CREATE TABLE`, so a test can assert how many times the schema was
// actually (re)created, not only that every caller returned nil.
func installCreateTableCounter(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE test_ddl_log (object_identity TEXT NOT NULL)`,
		`CREATE FUNCTION test_log_create_table() RETURNS event_trigger LANGUAGE plpgsql AS $$
		 DECLARE r record;
		 BEGIN
		   FOR r IN SELECT * FROM pg_event_trigger_ddl_commands() LOOP
		     INSERT INTO test_ddl_log VALUES (r.object_identity);
		   END LOOP;
		 END $$`,
		`CREATE EVENT TRIGGER test_count_create_table ON ddl_command_end
		 WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION test_log_create_table()`,
	} {
		_, err := db.Exec(stmt)
		require.NoError(t, err)
	}
}

// createdCount returns how many committed CREATE TABLEs named table.
func createdCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM test_ddl_log WHERE object_identity = $1`,
		"public."+table).Scan(&n))
	return n
}

// TestSchemaLock_ConcurrentEnsureSchema_FreshDB is the boot race: three
// processes call EnsureSchema on a database with no hookd schema at all.
// Several rounds with fresh prefixes make the unlocked race reproducible.
func TestSchemaLock_ConcurrentEnsureSchema_FreshDB(t *testing.T) {
	dsn := startPostgres(t)
	db := openDB(t, dsn)
	installCreateTableCounter(t, db)

	for round := 0; round < concurrentRounds; round++ {
		prefix := fmt.Sprintf("boot%d", round)
		errs := ensureConcurrently(t, dsn, prefix, concurrentBooters)
		for i, err := range errs {
			require.NoError(t, err, "round %d booter %d", round, i)
		}
		assertSchemaComplete(t, db, prefix)
		// Exactly ONE booter created the schema; the others re-checked under
		// the lock and found it current (a later booter re-running DROP+CREATE
		// would destroy what an earlier, already-running pod wrote).
		assert.Equal(t, 1, createdCount(t, db, prefix+"_hookd_subscriptions"),
			"round %d: schema must be created exactly once", round)
	}
	assert.Equal(t, 0, heldSchemaLocks(t, db), "no schema lock may outlive EnsureSchema")
}

// TestSchemaLock_ConcurrentEnsureSchema_Upgrade is the rolling-update race: the
// schema exists at an OLD version, so every booter takes the DROP+CREATE path.
func TestSchemaLock_ConcurrentEnsureSchema_Upgrade(t *testing.T) {
	dsn := startPostgres(t)
	db := openDB(t, dsn)
	installCreateTableCounter(t, db)

	for round := 0; round < concurrentRounds; round++ {
		prefix := fmt.Sprintf("upg%d", round)
		cfg, err := hookd.NewSchemaConfig(prefix)
		require.NoError(t, err)
		require.NoError(t, hookd.NewSchemaManager(db, cfg).EnsureSchema(context.Background()))
		_, err = db.Exec(fmt.Sprintf(`COMMENT ON TABLE %s IS 'go-hookd schema v0.1.0 - outdated'`, cfg.TableSubscriptions()))
		require.NoError(t, err)

		errs := ensureConcurrently(t, dsn, prefix, concurrentBooters)
		for i, err := range errs {
			require.NoError(t, err, "round %d booter %d", round, i)
		}
		assertSchemaComplete(t, db, prefix)
		// Once at setup + exactly ONE upgrade.
		assert.Equal(t, 2, createdCount(t, db, prefix+"_hookd_subscriptions"),
			"round %d: schema must be upgraded exactly once", round)
	}
}

// TestSchemaLock_TimeoutAndPrefixIsolation proves the wait is bounded, reported
// as a timeout, does not leak the lock, and that another prefix is not blocked.
func TestSchemaLock_TimeoutAndPrefixIsolation(t *testing.T) {
	dsn := startPostgres(t)
	db := openDB(t, dsn)
	ctx := context.Background()

	// Hold prefix "blocked"'s lock from an outside session.
	holder, err := db.Conn(ctx)
	require.NoError(t, err)
	classID, objectID := hookd.SchemaLockKey("blocked")
	_, err = holder.ExecContext(ctx, `SELECT pg_advisory_lock($1::int4, $2::int4)`, classID, objectID)
	require.NoError(t, err)

	// Another prefix proceeds without waiting.
	freeCfg, err := hookd.NewSchemaConfig("free")
	require.NoError(t, err)
	freeCtx, cancel := context.WithTimeout(ctx, otherPrefixDeadline)
	defer cancel()
	require.NoError(t, hookd.NewSchemaManager(db, freeCfg, hookd.WithSchemaLockTimeout(otherPrefixDeadline)).EnsureSchema(freeCtx))
	assertSchemaComplete(t, db, "free")

	// The held prefix times out, bounded, as a cuserr timeout.
	blockedCfg, err := hookd.NewSchemaConfig("blocked")
	require.NoError(t, err)
	blocked := hookd.NewSchemaManager(db, blockedCfg, hookd.WithSchemaLockTimeout(shortLockTimeout))
	began := time.Now()
	err = blocked.EnsureSchema(ctx)
	elapsed := time.Since(began)
	require.Error(t, err)
	assert.True(t, errors.Is(err, cuserr.ErrTimeout), "want a timeout error, got %v", err)
	assert.Contains(t, err.Error(), hookd.ErrMsgSchemaLockTimeout)
	assert.Less(t, elapsed, otherPrefixDeadline, "lock wait must be bounded by WithSchemaLockTimeout")

	// A cancelled context also ends the wait.
	cctx, ccancel := context.WithTimeout(ctx, shortLockTimeout)
	defer ccancel()
	err = hookd.NewSchemaManager(db, blockedCfg).EnsureSchema(cctx)
	require.Error(t, err)

	// Only the outside holder's lock remains: the failed waiters leaked nothing.
	assert.Equal(t, 1, heldSchemaLocks(t, db))

	// Release: the blocked prefix now succeeds and leaves no lock behind.
	_, err = holder.ExecContext(ctx, `SELECT pg_advisory_unlock($1::int4, $2::int4)`, classID, objectID)
	require.NoError(t, err)
	require.NoError(t, holder.Close())
	require.NoError(t, blocked.EnsureSchema(ctx))
	assertSchemaComplete(t, db, "blocked")
	require.NoError(t, blocked.DropSchema(ctx))
	assert.Equal(t, 0, heldSchemaLocks(t, db))
}

// TestSchemaManagerFromURL_HoldsNoIdleConnection proves the boot helper's pool
// is bounded to one connection and releases it after use.
func TestSchemaManagerFromURL_HoldsNoIdleConnection(t *testing.T) {
	dsn := startPostgres(t)
	cfg, err := hookd.NewSchemaConfig("footprint")
	require.NoError(t, err)

	m, err := hookd.NewSchemaManagerFromURL(dsn, cfg)
	require.NoError(t, err)
	defer func() { _ = m.Close() }()

	require.NoError(t, m.EnsureSchema(context.Background()))
	stats := m.DB().Stats()
	assert.Equal(t, hookd.SchemaManagerMaxOpenConns, stats.MaxOpenConnections)
	assert.Equal(t, 0, stats.OpenConnections, "no connection may be retained between calls")
}

// TestPostgresRepository_PoolOptionsReachThePool proves the options configure
// the repository's real pool against a live database.
func TestPostgresRepository_PoolOptionsReachThePool(t *testing.T) {
	dsn := startPostgres(t)
	db := openDB(t, dsn)
	cfg, err := hookd.NewSchemaConfig("pool")
	require.NoError(t, err)
	require.NoError(t, hookd.NewSchemaManager(db, cfg).EnsureSchema(context.Background()))

	repo, err := hookd.NewPostgresRepository(dsn,
		hookd.WithTablePrefix("pool"),
		hookd.WithMaxOpenConns(2),
		hookd.WithMaxIdleConns(1),
		hookd.WithConnMaxLifetime(time.Minute),
		hookd.WithConnMaxIdleTime(30*time.Second),
	)
	require.NoError(t, err)
	defer func() { _ = repo.Close() }()

	assert.Equal(t, 2, repo.PoolStats().MaxOpenConnections)
	require.NoError(t, repo.Ping(context.Background()))
}

// TestSchemaLock_ConcurrentEnsureSchema_DistinctPrefixes covers two consumers
// with DIFFERENT prefixes booting into one database: their per-prefix locks do
// not serialize them, but the schema batch runs a database-global
// `CREATE EXTENSION IF NOT EXISTS`, which races (duplicate pg_extension key)
// unless the DDL-level transaction lock serializes it.
func TestSchemaLock_ConcurrentEnsureSchema_DistinctPrefixes(t *testing.T) {
	dsn := startPostgres(t)
	db := openDB(t, dsn)

	for round := 0; round < concurrentRounds; round++ {
		// Make every round start with the extension absent.
		_, err := db.Exec(`DROP EXTENSION IF EXISTS "uuid-ossp"`)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), testOpTimeout)
		start := make(chan struct{})
		errs := make([]error, concurrentBooters)
		prefixes := make([]string, concurrentBooters)
		var wg sync.WaitGroup
		for i := 0; i < concurrentBooters; i++ {
			prefixes[i] = fmt.Sprintf("svc%d_r%d", i, round)
			cfg, err := hookd.NewSchemaConfig(prefixes[i])
			require.NoError(t, err)
			m, err := hookd.NewSchemaManagerFromURL(dsn, cfg)
			require.NoError(t, err)
			wg.Add(1)
			go func(i int, m *hookd.SchemaManager) {
				defer wg.Done()
				defer func() { _ = m.Close() }()
				<-start
				errs[i] = m.EnsureSchema(ctx)
			}(i, m)
		}
		close(start)
		wg.Wait()
		cancel()

		for i, err := range errs {
			require.NoError(t, err, "round %d prefix %s", round, prefixes[i])
			assertSchemaComplete(t, db, prefixes[i])
		}
	}
}
