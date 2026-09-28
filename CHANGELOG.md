# Changelog

All notable changes to go-hookd. Earlier releases are described in their tag
commit messages (`git log --tags`) and in README "Upgrading to vX" sections.

## v0.10.0 — 2026-09-28

Security fix. **Behaviour change — a cross-tenant lister must opt in explicitly.**

### Security
- **Tenant-less listings are now fail-CLOSED.** `Manager.ListDeliveries`,
  `PostgresRepository.ListDeliveries`, `PostgresRepositoryTx.ListDeliveries` and
  their `ListSubscriptions` siblings now REFUSE a filter whose `TenantID` is empty
  unless the new `AllTenants` opt-in is set, returning a validation error
  (`ErrMsgTenantScopeRequired`). Before this, an empty `TenantID` silently dropped
  the `WHERE tenant_id = $1` clause and returned **every tenant's** rows —
  deliveries include the webhook payload. A caller holding an org-less identity
  (e.g. a bare service-to-service credential; charonmw ≥ v0.23 no longer promotes
  such a caller to a system org) could enumerate the whole table. Reported against
  trove (`WebhookService.ListDeliveries`); the enforcing end was proven reachable
  on a live dev deployment.

### Added
- `DeliveryFilter.AllTenants bool` and `SubscriptionFilter.AllTenants bool` — the
  only way to scan across tenants. Omitted/false is per-tenant and fail-closed.
- `ErrMsgTenantScopeRequired` constant.

### ⚠ Consumer action required (only if you list across tenants)
An empty `TenantID` used to mean "every tenant". If any of your call sites relied
on that, set `AllTenants: true` explicitly there — it is a one-line change and it
makes the cross-tenant intent greppable. Per-tenant callers (the vast majority)
need **no change**: they already pass a `TenantID`. Known cross-tenant callers in
the fleet that must adopt the opt-in when they upgrade past their current pin:
- **deepr** `internal/webhook/dpr.webhook.dlq_sweeper.go` — the dead-letter
  redrive sweeper lists deliveries across all tenants (currently on go-hookd
  v0.9.0; unaffected until it bumps).
- **skope** `internal/webhook.bridge.internal.go` — the event dispatch bridge
  lists subscriptions across all tenants (retired; currently on v0.6.0).

`Manager.ListSubscriptions` previously refused an empty `TenantID` outright; it now
accepts one when `AllTenants` is set, so a deliberate cross-tenant subscription
scan is expressible without reopening the fail-open hole.

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
