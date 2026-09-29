# Changelog

All notable changes to go-hookd. Earlier releases are described in their tag
commit messages (`git log --tags`) and in README "Upgrading to vX" sections.

## v0.11.1 — 2026-09-29

Fixes found while converging agora onto go-hookd (vAudience/agora#28).

### Fixed
- **Idempotency never deduplicated on PostgreSQL.** `StoreIdempotencyKey` was an
  unconditional upsert, so `QueueDelivery`/`QueueInlineDelivery` with a repeated
  key queued a second delivery (only the mock refused). It now refuses a key
  that is still live (conflict → `NewIdempotencyError`) and takes over an
  expired one.
- **Every keyed `QueueInlineDelivery` failed on PostgreSQL.** The idempotency
  scope had a foreign key to subscriptions, and an inline delivery's scope is
  not a subscription. `EnsureSchema` drops that FK **in place** (no version
  bump; `schemaObsoleteConstraints`), and the inline scope is now
  `inline:` + sha256(tenant, URL) — fixed length (a URL could exceed the column)
  and tenant-separated (one tenant's key no longer suppresses another's).
- **A redrive re-used attempt numbers.** `RequeueDeadLetter` reset
  `attempt_count` to 0, so the next attempt collided with the unique
  (delivery, attempt_number) index (its record was lost) and
  `X-Webhook-Attempt` repeated. The count now keeps rising and the budget is
  renewed (`max_attempts += original`).

- **A failed delivery insert stranded its idempotency key.** The key was stored
  before the row; if the insert failed the key stayed live and the caller's
  retry was refused as a duplicate — the delivery silently lost. Keyed
  deliveries are now created first, HELD (not yet due), then the key is stored;
  a duplicate or error deletes the held row, success releases it.
- **Redrive budgets compounded.** `max_attempts` now renews by the ORIGINAL
  budget, persisted in the new nullable `deliveries.attempt_budget` column
  (added in place), and backoff restarts within each budget.
- `DeleteSubscription` deletes the subscription's idempotency keys in the same
  statement (the dropped FK used to cascade them). Inline scopes expire by TTL.
- The obsolete FK is found by structure (foreign key idempotency → subscriptions),
  not by name.

### Added
- `WithoutCircuitBreaker()`: no breaker state is read or written — for a
  consumer with its own breaker; hookd's breaker rows are keyed by endpoint URL
  and carry no tenant.

### Security
- Egress refuses ORCHID `2001:10::/28` and ORCHIDv2 `2001:20::/28`.

### ⚠ Behaviour changes
- A repeated live idempotency key is now refused on PostgreSQL (as documented
  all along) — callers that relied on duplicates being accepted will see
  `IsIdempotencyError`.
- `RetryDeadLetter` no longer zeroes `AttemptCount`.
- **Mixed v0.11.0/v0.11.1 fleets give no dedupe guarantee** during the rollout
  (v0.11.0 still upserts, and uses the bare-URL inline scope). Schema changes
  are safe both ways.

### Known limits
- Table prefixes above ~20 characters can make generated index names collide
  after PostgreSQL's 63-byte truncation (pre-existing; tracked separately).

## v0.11.0 — 2026-09-28

Correctness and security. **Consumer action is required only as listed under
"⚠ Consumer action"**; see README "Upgrading to v0.11.0".

### Fixed
- **Duplicate delivery under concurrent workers (go-hookd#1).** The poll was an
  autocommit `SELECT ... FOR UPDATE SKIP LOCKED`: the row lock ended with the
  statement, so the row was unlocked and still `pending` before the worker sent
  it, and two non-overlapping polls could both deliver it. Claims are now
  **leases**: one `UPDATE ... SET next_retry_at = now + lease` over a
  `FOR UPDATE SKIP LOCKED` subselect (`ClaimPendingDeliveries`), so the claim
  survives the statement; the claimed `next_retry_at` is a strictly increasing
  **fencing token**, re-checked by `RenewDeliveryClaim` immediately before the
  HTTP send — a worker whose claim lapsed and was re-taken skips the row instead
  of sending it. No schema change, no reaper (a crashed worker's rows become due
  when the lease runs out). Proven against real PostgreSQL by barrier-released
  concurrent claimers and concurrent Managers (`./integration/`).
- Post-send bookkeeping (attempt record, status, dead letter) runs on a context
  detached from shutdown, so a sent webhook is always recorded; claimed but
  unstarted rows are released on `Stop`; an open circuit breaker parks a row
  until its retry time instead of re-polling it every interval.
- **`X-Webhook-Idempotency-Key` was declared and never sent (go-hookd#2).** The
  queuer's `IdempotencyKey` is now stored on the delivery
  (`Delivery.IdempotencyKey`, nullable `deliveries.idempotency_key`) and sent on
  every attempt; absent when none was given.
- **User-Agent version drift (go-hookd#2):** v0.7.2–v0.10.0 announced stale
  versions. The version now comes from the binary's build info; the fallback
  constant is pinned to `versions.yaml` by a test.
- Response bodies from subscriber endpoints are read at most
  `MaxResponseBodyLength`+1 bytes (was unbounded; only 10 KB was ever kept).

### Security
- **Cleanup is fail-closed on tenant scope (go-hookd#7).** `CleanupFilter` gains
  `AllTenants`; `Manager.CleanupDeliveries`, `CleanupFilter.Validate` and every
  `Count/DeleteDeliveriesByFilter` (postgres, tx, mock) refuse an empty
  `TenantID` without it (`ErrMsgTenantScopeRequired`), and a nil filter. Before,
  an empty tenant deleted — and the dry run counted — every tenant's deliveries.
- **Egress guard can no longer be waived (go-hookd#3).**
  `WithAllowPrivateDestinations()` used to also accept a `WithHTTPClient` client
  the guard cannot wrap and use it UNGUARDED (link-local / cloud metadata
  included). Such a client is now always a configuration error; the opt-in only
  ever re-admits loopback, RFC 1918/ULA and CGNAT. Source pins (AST tests) keep
  `allowPrivate` settable only by that option and the delivery client buildable
  only by `buildHTTPClient`. Default transport gains a 10s TLS handshake timeout.

### Added
- **`SecretResolver` / `WithSecretResolver(r)` (go-hookd#3).** The stored secret
  becomes a reference, resolved on EVERY attempt and TestSubscription (never
  cached); a failed resolve sends nothing and records the opaque
  `ErrMsgSigningSecretUnavailable` (cause logged, never stored). Default
  `StoredSecretResolver` keeps today's behaviour. `SecretResolverFunc` adapter.
  The resolver receives a `SecretRequest` — `Ref` (the stored column) plus the
  ROW's `TenantID`, `SubscriptionID`, `DeliveryID` and `IdempotencyKey` — so a
  tenancy-scoped resolver (agora's Tresor lookup) never trusts in-flight input.
  Its error is logged by type only (the text may name a vault path).
- `DeliveryEvent.IdempotencyKey`, so a consumer queueing from its own outbox can
  map `delivery.success` / `delivery.dead_letter` back to its row.
- `Config.ClaimLeaseMs` (0 = `DeliveryTimeoutMs` + 60s; explicit values must be
  ≥ `DeliveryTimeoutMs` + 15s), `Config.ClaimLease()`, `ErrDeliveryClaimLost`.
- `SchemaManager.EnsureSchema` applies **additive nullable columns in place**
  (`schemaAdditiveColumns`, under the schema lock) instead of bumping
  `SchemaVersion` — a bump DROPs every table, and an older binary booting during
  a rolling deploy would drop the upgraded schema.

### Hardened after independent code + security review
- The attempt deadline is measured from BEFORE the pre-send renewal (renewal
  bounded by `ClaimRenewTimeout` = 2s < the 5s margin), so a slow renewal cannot
  push an attempt past its lease.
- `RetryDeadLetter` → one conditional `RequeueDeadLetter` statement (new
  Repository method); concurrent redrives re-queue once (`ErrDeliveryNotDeadLetter`).
- Rows whose pre-send reads/renewal are cut by `Stop` are released, not left leased.
- `Config.EffectiveBatchSize()` caps a poll to what one lease covers (WARN when
  it clamps `MaxBatchSize`).
- `IdempotencyKey` is validated at queue time (≤255 visible ASCII): it is now a
  header, and an invalid one would have failed every attempt.
- Resolver: a panic or an empty secret from a custom resolver fails the attempt
  (nothing sent); a subscription delivery whose tenant differs from its
  subscription's is refused before `Resolve`; `SecretRequest` tenancy comes from
  the secret-bearing row.
- Repository `Count/DeleteDeliveriesByFilter` run the full `CleanupFilter.Validate`
  (so `AllTenants` alone can never mean "the whole table").
- Egress: response header block capped (16 KiB) and stored headers bounded
  (32 keys, 512 B each, first value); stored body/headers forced to valid UTF-8;
  a consumer client's cookie jar is dropped; Teredo, SIIT, SRv6 SID and RFC 9637
  ranges refused; a subscriber custom header can't spoof
  `X-Webhook-Idempotency-Key`; TestSubscription errors are fixed strings and an
  unknown id takes no rate-limit slot.
- Schema existence checks resolve the table with `to_regclass` — through
  `search_path`, exactly like the unqualified DDL — instead of hardcoding
  `'public'` (with tables outside public, the check never matched and EnsureSchema
  re-ran the destructive create path every boot); the additive ALTER runs with
  a 5s `lock_timeout`.
- The delivery record is written before the circuit-breaker update.

### Changed
- `Config.MaxBatchSize` is honoured (it was ignored); default changed 100 → 1,
  preserving the old effective behaviour.

### ⚠ Consumer action required
- **Custom `Repository` implementations:** replace `GetPendingDeliveries` with
  `ClaimPendingDeliveries`, `RenewDeliveryClaim`, `ReleaseDeliveryClaim`; add
  `RequeueDeadLetter`.
- **Idempotency keys are validated at queue time:** a key with a control
  character (CR, LF, TAB, …) or non-ASCII byte, or longer than 255 bytes, is now
  refused by `QueueDelivery` / `QueueInlineDelivery` (it is sent as a header,
  where it would have failed every attempt). Spaces are allowed.
- **Rollout:** run `EnsureSchema` first; roll webhook workers over together (a
  v0.10 worker's unfenced poll can double-send alongside a v0.11 one).
- **Fleet-wide `CleanupDeliveries` callers:** set `CleanupFilter.AllTenants`.
  (No known fleet caller: deepr calls only `GetMaintenanceStats`.)
- **`WithHTTPClient` with an opaque RoundTripper (+ `WithAllowPrivateDestinations`):**
  pass an `*http.Transport` instead. (No known fleet caller.)
- **deepr** sets `MaxBatchSize: 200`; it is clamped to 2 at the default lease
  (WARN at boot). Set a small value explicitly to silence it.

### Notes
- `PostgresRepositoryTx`'s CRUD methods still use the pre-v0.6.0 unprefixed
  table names (they have not worked since v0.6.0; no production path calls
  `BeginTx`). The new claim and the cleanup methods on it are correct. Tracked
  separately.

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
