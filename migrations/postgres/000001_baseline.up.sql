-- Migration: 000001_baseline
-- Description: Baseline schema for go-hookd v0.3.0 webhook management system
-- Author: go-hookd
-- Created: 2025-12-20
-- Schema Version: 1 (see versions.yaml)

-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- =============================================================================
-- SUBSCRIPTIONS TABLE
-- =============================================================================
-- Stores webhook subscription configurations
CREATE TABLE IF NOT EXISTS subscriptions (
    -- Identity
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL,

    -- Endpoint configuration
    url TEXT NOT NULL,
    secret VARCHAR(512) NOT NULL, -- HMAC secret for signing

    -- Event filtering
    event_types TEXT[] NOT NULL,

    -- Metadata filtering (NEW in v0.3.0)
    filters JSONB DEFAULT '{}'::jsonb,

    -- Subscription state
    status VARCHAR(50) NOT NULL DEFAULT 'active',

    -- Retry configuration (stored as JSONB for flexibility)
    retry_policy JSONB NOT NULL DEFAULT '{
        "max_attempts": 10,
        "initial_backoff": "1s",
        "max_backoff": "1h",
        "backoff_factor": 2.0
    }'::jsonb,

    -- Custom headers for delivery
    headers JSONB DEFAULT '{}'::jsonb,

    -- Additional metadata
    metadata JSONB DEFAULT '{}'::jsonb,

    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    -- Constraints
    CONSTRAINT chk_subscription_status CHECK (status IN ('active', 'paused', 'disabled')),
    CONSTRAINT chk_url_not_empty CHECK (LENGTH(TRIM(url)) > 0),
    CONSTRAINT chk_secret_not_empty CHECK (LENGTH(TRIM(secret)) > 0),
    CONSTRAINT chk_event_types_not_empty CHECK (array_length(event_types, 1) > 0)
);

-- Indexes for subscriptions
CREATE INDEX idx_subscriptions_tenant_id ON subscriptions(tenant_id);
CREATE INDEX idx_subscriptions_status ON subscriptions(status);
CREATE INDEX idx_subscriptions_event_types ON subscriptions USING GIN(event_types);
CREATE INDEX idx_subscriptions_created_at ON subscriptions(created_at DESC);
CREATE UNIQUE INDEX idx_subscriptions_tenant_url ON subscriptions(tenant_id, url);
CREATE INDEX idx_subscriptions_filters ON subscriptions USING GIN(filters);

-- Comments
COMMENT ON TABLE subscriptions IS 'Webhook subscription configurations';
COMMENT ON COLUMN subscriptions.id IS 'Prefixed nanoID (sub_*)';
COMMENT ON COLUMN subscriptions.tenant_id IS 'Tenant identifier for multi-tenancy';
COMMENT ON COLUMN subscriptions.event_types IS 'Array of event types this subscription listens to';
COMMENT ON COLUMN subscriptions.filters IS 'JSONB filter conditions for metadata-based filtering';
COMMENT ON COLUMN subscriptions.retry_policy IS 'Retry configuration as JSONB';

-- =============================================================================
-- DELIVERIES TABLE
-- =============================================================================
-- Stores webhook delivery queue and state
-- Supports both subscription-based and inline (ad-hoc) deliveries
CREATE TABLE IF NOT EXISTS deliveries (
    -- Identity
    id VARCHAR(255) PRIMARY KEY,
    subscription_id VARCHAR(255), -- NULLABLE for inline deliveries
    tenant_id VARCHAR(255) NOT NULL,

    -- Event data
    event_type VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,

    -- Inline delivery fields (only used when subscription_id is NULL)
    url TEXT,                    -- Direct URL for inline deliveries
    secret VARCHAR(512),         -- HMAC secret for inline deliveries

    -- Delivery state
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL,

    -- Retry scheduling
    next_retry_at TIMESTAMP WITH TIME ZONE,

    -- Completion tracking
    completed_at TIMESTAMP WITH TIME ZONE,

    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    -- Constraints
    CONSTRAINT chk_delivery_status CHECK (status IN ('pending', 'success', 'failed', 'dead_letter')),
    CONSTRAINT chk_attempt_count_positive CHECK (attempt_count >= 0),
    CONSTRAINT chk_max_attempts_positive CHECK (max_attempts >= 0),
    CONSTRAINT chk_delivery_target CHECK (subscription_id IS NOT NULL OR url IS NOT NULL),

    -- Foreign keys (optional for inline deliveries)
    CONSTRAINT fk_deliveries_subscription
        FOREIGN KEY (subscription_id)
        REFERENCES subscriptions(id)
        ON DELETE CASCADE
);

-- Indexes for deliveries (optimized for queue processing with SKIP LOCKED)
CREATE INDEX idx_deliveries_subscription_id ON deliveries(subscription_id);
CREATE INDEX idx_deliveries_tenant_id ON deliveries(tenant_id);
CREATE INDEX idx_deliveries_status ON deliveries(status);
CREATE INDEX idx_deliveries_event_type ON deliveries(event_type);
CREATE INDEX idx_deliveries_created_at ON deliveries(created_at DESC);

-- Critical index for queue processing with SKIP LOCKED
-- This composite index ensures efficient pending delivery retrieval
CREATE INDEX idx_deliveries_pending_queue ON deliveries(status, next_retry_at, created_at)
    WHERE status = 'pending';

-- Index for inline deliveries (deliveries without subscription)
CREATE INDEX idx_deliveries_inline ON deliveries(tenant_id, event_type)
    WHERE subscription_id IS NULL;

-- Comments
COMMENT ON TABLE deliveries IS 'Webhook delivery queue and history';
COMMENT ON COLUMN deliveries.id IS 'Prefixed nanoID (dlv_*)';
COMMENT ON COLUMN deliveries.subscription_id IS 'NULL for inline deliveries';
COMMENT ON COLUMN deliveries.url IS 'Direct URL for inline deliveries (NULL for subscription-based)';
COMMENT ON COLUMN deliveries.secret IS 'HMAC secret for inline deliveries (NULL for subscription-based)';
COMMENT ON COLUMN deliveries.status IS 'Current delivery status';
COMMENT ON COLUMN deliveries.next_retry_at IS 'Scheduled time for next retry attempt';
COMMENT ON INDEX idx_deliveries_pending_queue IS 'Optimized for SKIP LOCKED queue processing';
COMMENT ON CONSTRAINT chk_delivery_target ON deliveries IS 'Ensures delivery has either subscription_id OR url for inline delivery';

-- =============================================================================
-- DELIVERY_ATTEMPTS TABLE
-- =============================================================================
-- Stores history of all delivery attempts
CREATE TABLE IF NOT EXISTS delivery_attempts (
    -- Identity
    id VARCHAR(255) PRIMARY KEY,
    delivery_id VARCHAR(255) NOT NULL,

    -- Attempt tracking
    attempt_number INTEGER NOT NULL,

    -- HTTP response
    status_code INTEGER NOT NULL DEFAULT 0,
    response_body TEXT,
    response_headers JSONB DEFAULT '{}'::jsonb,

    -- Error tracking
    error TEXT,

    -- Timing metrics (NEW in v0.3.0)
    duration_ms BIGINT DEFAULT 0,

    -- Timestamps
    attempted_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    -- Constraints
    CONSTRAINT chk_attempt_number_positive CHECK (attempt_number > 0),
    CONSTRAINT chk_status_code_valid CHECK (status_code >= 0 AND status_code < 600),
    CONSTRAINT chk_duration_ms_positive CHECK (duration_ms >= 0),

    -- Foreign keys
    CONSTRAINT fk_attempts_delivery
        FOREIGN KEY (delivery_id)
        REFERENCES deliveries(id)
        ON DELETE CASCADE
);

-- Indexes for delivery attempts
CREATE INDEX idx_attempts_delivery_id ON delivery_attempts(delivery_id);
CREATE INDEX idx_attempts_attempted_at ON delivery_attempts(attempted_at DESC);
CREATE INDEX idx_attempts_status_code ON delivery_attempts(status_code);
CREATE UNIQUE INDEX idx_attempts_delivery_number ON delivery_attempts(delivery_id, attempt_number);

-- Comments
COMMENT ON TABLE delivery_attempts IS 'History of all webhook delivery attempts';
COMMENT ON COLUMN delivery_attempts.id IS 'Prefixed nanoID (att_*)';
COMMENT ON COLUMN delivery_attempts.attempt_number IS '1-based attempt number';
COMMENT ON COLUMN delivery_attempts.status_code IS 'HTTP status code (0 if network error)';
COMMENT ON COLUMN delivery_attempts.duration_ms IS 'Request duration in milliseconds';

-- =============================================================================
-- IDEMPOTENCY_STORE TABLE
-- =============================================================================
-- Tracks idempotency keys to prevent duplicate deliveries
CREATE TABLE IF NOT EXISTS idempotency_store (
    -- Composite primary key
    idempotency_key VARCHAR(255) NOT NULL,
    subscription_id VARCHAR(255) NOT NULL,

    -- Expiration
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,

    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    -- Primary key
    PRIMARY KEY (idempotency_key, subscription_id),

    -- Foreign keys
    CONSTRAINT fk_idempotency_subscription
        FOREIGN KEY (subscription_id)
        REFERENCES subscriptions(id)
        ON DELETE CASCADE
);

-- Indexes for idempotency store
CREATE INDEX idx_idempotency_expires_at ON idempotency_store(expires_at);
CREATE INDEX idx_idempotency_subscription_id ON idempotency_store(subscription_id);

-- Comments
COMMENT ON TABLE idempotency_store IS 'Tracks idempotency keys for exactly-once delivery';
COMMENT ON COLUMN idempotency_store.expires_at IS 'Expiration time for automatic cleanup';

-- =============================================================================
-- CIRCUIT_BREAKER_STATE TABLE
-- =============================================================================
-- Stores circuit breaker state per endpoint
CREATE TABLE IF NOT EXISTS circuit_breaker_state (
    -- Identity (endpoint URL is the key)
    endpoint TEXT PRIMARY KEY,

    -- Circuit state
    state VARCHAR(50) NOT NULL DEFAULT 'closed',

    -- Failure tracking
    failure_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,

    -- Timestamps
    last_failure TIMESTAMP WITH TIME ZONE,
    opened_at TIMESTAMP WITH TIME ZONE,
    next_retry_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    -- Constraints
    CONSTRAINT chk_circuit_state CHECK (state IN ('closed', 'half_open', 'open')),
    CONSTRAINT chk_failure_count_positive CHECK (failure_count >= 0),
    CONSTRAINT chk_success_count_positive CHECK (success_count >= 0)
);

-- Indexes for circuit breaker
CREATE INDEX idx_circuit_breaker_state ON circuit_breaker_state(state);
CREATE INDEX idx_circuit_breaker_next_retry ON circuit_breaker_state(next_retry_at)
    WHERE state = 'open';

-- Comments
COMMENT ON TABLE circuit_breaker_state IS 'Circuit breaker state per webhook endpoint';
COMMENT ON COLUMN circuit_breaker_state.state IS 'Current circuit state: closed (normal), half_open (testing), open (failing)';
COMMENT ON COLUMN circuit_breaker_state.failure_count IS 'Consecutive failure count';
COMMENT ON COLUMN circuit_breaker_state.success_count IS 'Consecutive success count (used in half_open)';

-- =============================================================================
-- FUNCTIONS AND TRIGGERS
-- =============================================================================

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Trigger for subscriptions.updated_at
CREATE TRIGGER trg_subscriptions_updated_at
    BEFORE UPDATE ON subscriptions
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Trigger for circuit_breaker_state.updated_at
CREATE TRIGGER trg_circuit_breaker_updated_at
    BEFORE UPDATE ON circuit_breaker_state
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Function to automatically clean up expired idempotency keys
CREATE OR REPLACE FUNCTION cleanup_expired_idempotency_keys()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    DELETE FROM idempotency_store
    WHERE expires_at < NOW();

    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

-- Comments
COMMENT ON FUNCTION update_updated_at_column() IS 'Automatically updates updated_at timestamp on row modification';
COMMENT ON FUNCTION cleanup_expired_idempotency_keys() IS 'Removes expired idempotency keys (call periodically via cron)';

-- =============================================================================
-- GRANTS (Optional - adjust based on your security requirements)
-- =============================================================================
-- GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO hookd_app;
-- GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO hookd_app;

-- =============================================================================
-- MIGRATION COMPLETE
-- =============================================================================
-- Tables created: 5
-- Indexes created: 26
-- Functions created: 2
-- Triggers created: 2
-- Schema version: 1 (baseline for v0.3.0)
