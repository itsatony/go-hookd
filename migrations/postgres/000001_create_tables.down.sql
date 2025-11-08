-- Migration: 000001_create_tables (ROLLBACK)
-- Description: Clean rollback of all go-hookd tables and functions
-- Author: go-hookd
-- Created: 2025-01-08

-- =============================================================================
-- DROP TRIGGERS
-- =============================================================================

DROP TRIGGER IF EXISTS trg_circuit_breaker_updated_at ON circuit_breaker_state;
DROP TRIGGER IF EXISTS trg_subscriptions_updated_at ON subscriptions;

-- =============================================================================
-- DROP FUNCTIONS
-- =============================================================================

DROP FUNCTION IF EXISTS cleanup_expired_idempotency_keys();
DROP FUNCTION IF EXISTS update_updated_at_column();

-- =============================================================================
-- DROP TABLES
-- =============================================================================
-- Order matters: drop tables with foreign keys first, then referenced tables

-- Drop delivery attempts (references deliveries)
DROP TABLE IF EXISTS delivery_attempts CASCADE;

-- Drop deliveries (references subscriptions)
DROP TABLE IF EXISTS deliveries CASCADE;

-- Drop idempotency store (references subscriptions)
DROP TABLE IF EXISTS idempotency_store CASCADE;

-- Drop circuit breaker state (no dependencies)
DROP TABLE IF EXISTS circuit_breaker_state CASCADE;

-- Drop subscriptions (referenced by other tables)
DROP TABLE IF EXISTS subscriptions CASCADE;

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
