-- Migration: 000001_baseline (ROLLBACK)
-- Description: Clean rollback of all go-hookd tables and functions
-- Author: go-hookd
-- Created: 2025-12-20
-- Schema Version: 2 (see versions.yaml)
--
-- IMPORTANT: All database objects use the "hookd_" prefix to prevent
-- namespace collisions when go-hookd is embedded in other applications.

-- =============================================================================
-- DROP TRIGGERS
-- =============================================================================

DROP TRIGGER IF EXISTS trg_hookd_circuit_breaker_updated_at ON hookd_circuit_breaker_state;
DROP TRIGGER IF EXISTS trg_hookd_subscriptions_updated_at ON hookd_subscriptions;

-- =============================================================================
-- DROP FUNCTIONS
-- =============================================================================

DROP FUNCTION IF EXISTS hookd_cleanup_expired_idempotency();
DROP FUNCTION IF EXISTS hookd_update_updated_at();

-- =============================================================================
-- DROP TABLES
-- =============================================================================
-- Order matters: drop tables with foreign keys first, then referenced tables

-- Drop delivery attempts (references deliveries)
DROP TABLE IF EXISTS hookd_delivery_attempts CASCADE;

-- Drop deliveries (references subscriptions)
DROP TABLE IF EXISTS hookd_deliveries CASCADE;

-- Drop idempotency store (references subscriptions)
DROP TABLE IF EXISTS hookd_idempotency_store CASCADE;

-- Drop circuit breaker state (no dependencies)
DROP TABLE IF EXISTS hookd_circuit_breaker_state CASCADE;

-- Drop subscriptions (referenced by other tables)
DROP TABLE IF EXISTS hookd_subscriptions CASCADE;

-- =============================================================================
-- DROP EXTENSIONS (OPTIONAL)
-- =============================================================================
-- Uncomment if you want to drop extensions as well
-- Note: Only drop if no other schemas/tables are using these extensions

-- DROP EXTENSION IF EXISTS "uuid-ossp";

-- =============================================================================
-- ROLLBACK COMPLETE
-- =============================================================================
-- All go-hookd tables, indexes, triggers, and functions have been removed
