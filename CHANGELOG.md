# Changelog

All notable changes to go-hookd. Earlier releases are described in their tag
commit messages (`git log --tags`) and in README "Upgrading to vX" sections.

## v0.9.0 — 2026-09-27

Additive; no consumer change required.

### Added
- `PostgresRepository` pool options: `WithMaxOpenConns(n)`, `WithMaxIdleConns(n)`,
  `WithConnMaxLifetime(d)`, `WithConnMaxIdleTime(d)`. Defaults are the values
  previously hardcoded (25 / 5 / 5m / 1m, now `DefaultPostgres*` constants).
  Validation: open `> 0`, idle `>= 0`, durations `> 0`; an explicit idle above
  the effective open count is refused, the default idle is clamped to it.
  Validated before any connection is opened.
- `PostgresRepository.PoolStats()` returns the pool's `sql.DBStats`.
- `SchemaManagerOption` + `WithSchemaLockTimeout(d)` (default 60s), accepted as a
  variadic argument by `NewSchemaManager` and `NewSchemaManagerFromURL`.
- `SchemaLockKey(prefix)` exposes the advisory-lock keys for diagnostics.
- `make test-integration-isolated` / `./integration/`: real-PostgreSQL tests for
  concurrent boot (fresh DB, version upgrade, distinct prefixes), lock timeout,
  prefix isolation, lock-leak checks and pool footprint.

### Fixed
- **Concurrent-boot race in schema setup.** `EnsureSchema` and `DropSchema` now
  run under a session-level `pg_advisory_lock(SchemaLockClassID, hash(prefix))` on
  a dedicated connection, bounded by `lock_timeout` + context, re-check the schema
  version after acquiring, and release on every path. Several processes booting
  together previously all ran the DROP/CREATE batch and failed with
  `deadlock detected` / duplicate `pg_extension` / `pg_type` keys, or re-created a
  schema another process had just created. The DDL batch additionally takes a
  transaction-level lock shared by all prefixes, because
  `CREATE EXTENSION IF NOT EXISTS` is database-global.
- **`NewSchemaManagerFromURL` held a pool for the service lifetime.** Its pool is
  now bounded to 1 open / 0 idle connections, so it holds no connection between
  calls.

### Notes
- Session advisory locks need a direct connection or a session-mode pooler.
- Known, not changed here: the ROOT package's `-tags=integration` build
  (`make test-integration`) has not compiled since v0.6.0 (stale test files
  reference removed APIs). The new tests live in `./integration/` so they build
  independently.
