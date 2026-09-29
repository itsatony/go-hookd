
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

DROP TRIGGER IF EXISTS trg_ago_hookd_subscriptions_updated_at ON ago_hookd_subscriptions;
DROP TRIGGER IF EXISTS trg_ago_hookd_circuit_breaker_updated_at ON ago_hookd_circuit_breaker_state;

DROP FUNCTION IF EXISTS ago_hookd_update_updated_at() CASCADE;
DROP FUNCTION IF EXISTS ago_hookd_cleanup_expired_idempotency() CASCADE;

DROP TABLE IF EXISTS ago_hookd_delivery_attempts CASCADE;
DROP TABLE IF EXISTS ago_hookd_idempotency_store CASCADE;
DROP TABLE IF EXISTS ago_hookd_deliveries CASCADE;
DROP TABLE IF EXISTS ago_hookd_circuit_breaker_state CASCADE;
DROP TABLE IF EXISTS ago_hookd_subscriptions CASCADE;

CREATE TABLE ago_hookd_subscriptions (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL,

    url TEXT NOT NULL,
    secret VARCHAR(512) NOT NULL, -- HMAC secret for signing

    event_types TEXT[] NOT NULL,

    filters JSONB DEFAULT '{}'::jsonb,

    status VARCHAR(50) NOT NULL DEFAULT 'active',

    retry_policy JSONB NOT NULL DEFAULT '{
        "max_attempts": 10,
        "initial_backoff": "1s",
        "max_backoff": "1h",
        "backoff_factor": 2.0
    }'::jsonb,

    headers JSONB DEFAULT '{}'::jsonb,

    metadata JSONB DEFAULT '{}'::jsonb,

    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_ago_hookd_subscriptions_status CHECK (status IN ('active', 'paused', 'disabled')),
    CONSTRAINT chk_ago_hookd_subscriptions_url CHECK (LENGTH(TRIM(url)) > 0),
    CONSTRAINT chk_ago_hookd_subscriptions_secret CHECK (LENGTH(TRIM(secret)) > 0),
    CONSTRAINT chk_ago_hookd_subscriptions_events CHECK (array_length(event_types, 1) > 0)
);

CREATE INDEX idx_ago_hookd_subscriptions_tenant_id ON ago_hookd_subscriptions(tenant_id);
CREATE INDEX idx_ago_hookd_subscriptions_status ON ago_hookd_subscriptions(status);
CREATE INDEX idx_ago_hookd_subscriptions_event_types ON ago_hookd_subscriptions USING GIN(event_types);
CREATE INDEX idx_ago_hookd_subscriptions_created_at ON ago_hookd_subscriptions(created_at DESC);
CREATE UNIQUE INDEX idx_ago_hookd_subscriptions_tenant_url ON ago_hookd_subscriptions(tenant_id, url);
CREATE INDEX idx_ago_hookd_subscriptions_filters ON ago_hookd_subscriptions USING GIN(filters);

COMMENT ON TABLE ago_hookd_subscriptions IS 'go-hookd schema v0.6.0 - Webhook subscription configurations';
COMMENT ON COLUMN ago_hookd_subscriptions.id IS 'Prefixed nanoID (sub_*)';
COMMENT ON COLUMN ago_hookd_subscriptions.tenant_id IS 'Tenant identifier for multi-tenancy';
COMMENT ON COLUMN ago_hookd_subscriptions.event_types IS 'Array of event types this subscription listens to';
COMMENT ON COLUMN ago_hookd_subscriptions.filters IS 'JSONB filter conditions for metadata-based filtering';
COMMENT ON COLUMN ago_hookd_subscriptions.retry_policy IS 'Retry configuration as JSONB';

CREATE TABLE ago_hookd_deliveries (
    id VARCHAR(255) PRIMARY KEY,
    subscription_id VARCHAR(255), -- NULLABLE for inline deliveries
    tenant_id VARCHAR(255) NOT NULL,

    event_type VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,

    url TEXT,                    -- Direct URL for inline deliveries
    secret VARCHAR(512),         -- HMAC secret for inline deliveries

    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL,

    next_retry_at TIMESTAMP WITH TIME ZONE,

    completed_at TIMESTAMP WITH TIME ZONE,

    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    idempotency_key VARCHAR(255),

    attempt_budget INTEGER,

    CONSTRAINT chk_ago_hookd_deliveries_status CHECK (status IN ('pending', 'success', 'failed', 'dead_letter')),
    CONSTRAINT chk_ago_hookd_deliveries_attempts CHECK (attempt_count >= 0),
    CONSTRAINT chk_ago_hookd_deliveries_max_attempts CHECK (max_attempts >= 0),
    CONSTRAINT chk_ago_hookd_deliveries_target CHECK (subscription_id IS NOT NULL OR url IS NOT NULL),

    CONSTRAINT fk_ago_hookd_deliveries_subscription
        FOREIGN KEY (subscription_id)
        REFERENCES ago_hookd_subscriptions(id)
        ON DELETE CASCADE
);

CREATE INDEX idx_ago_hookd_deliveries_subscription_id ON ago_hookd_deliveries(subscription_id);
CREATE INDEX idx_ago_hookd_deliveries_tenant_id ON ago_hookd_deliveries(tenant_id);
CREATE INDEX idx_ago_hookd_deliveries_status ON ago_hookd_deliveries(status);
CREATE INDEX idx_ago_hookd_deliveries_event_type ON ago_hookd_deliveries(event_type);
CREATE INDEX idx_ago_hookd_deliveries_created_at ON ago_hookd_deliveries(created_at DESC);

CREATE INDEX idx_ago_hookd_deliveries_pending_queue ON ago_hookd_deliveries(status, next_retry_at, created_at)
    WHERE status = 'pending';

CREATE INDEX idx_ago_hookd_deliveries_inline ON ago_hookd_deliveries(tenant_id, event_type)
    WHERE subscription_id IS NULL;

COMMENT ON TABLE ago_hookd_deliveries IS 'Webhook delivery queue and history';
COMMENT ON COLUMN ago_hookd_deliveries.id IS 'Prefixed nanoID (dlv_*)';
COMMENT ON COLUMN ago_hookd_deliveries.subscription_id IS 'NULL for inline deliveries';
COMMENT ON COLUMN ago_hookd_deliveries.url IS 'Direct URL for inline deliveries (NULL for subscription-based)';
COMMENT ON COLUMN ago_hookd_deliveries.secret IS 'HMAC secret for inline deliveries (NULL for subscription-based)';
COMMENT ON COLUMN ago_hookd_deliveries.status IS 'Current delivery status';
COMMENT ON COLUMN ago_hookd_deliveries.next_retry_at IS 'Scheduled time for next retry attempt';
COMMENT ON INDEX idx_ago_hookd_deliveries_pending_queue IS 'Optimized for SKIP LOCKED queue processing';
COMMENT ON CONSTRAINT chk_ago_hookd_deliveries_target ON ago_hookd_deliveries IS 'Ensures delivery has either subscription_id OR url for inline delivery';

CREATE TABLE ago_hookd_delivery_attempts (
    id VARCHAR(255) PRIMARY KEY,
    delivery_id VARCHAR(255) NOT NULL,

    attempt_number INTEGER NOT NULL,

    status_code INTEGER NOT NULL DEFAULT 0,
    response_body TEXT,
    response_headers JSONB DEFAULT '{}'::jsonb,

    error TEXT,

    duration_ms BIGINT DEFAULT 0,

    attempted_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_ago_hookd_attempts_number CHECK (attempt_number > 0),
    CONSTRAINT chk_ago_hookd_attempts_status CHECK (status_code >= 0 AND status_code < 600),
    CONSTRAINT chk_ago_hookd_attempts_duration CHECK (duration_ms >= 0),

    CONSTRAINT fk_ago_hookd_attempts_delivery
        FOREIGN KEY (delivery_id)
        REFERENCES ago_hookd_deliveries(id)
        ON DELETE CASCADE
);

CREATE INDEX idx_ago_hookd_attempts_delivery_id ON ago_hookd_delivery_attempts(delivery_id);
CREATE INDEX idx_ago_hookd_attempts_attempted_at ON ago_hookd_delivery_attempts(attempted_at DESC);
CREATE INDEX idx_ago_hookd_attempts_status_code ON ago_hookd_delivery_attempts(status_code);
CREATE UNIQUE INDEX idx_ago_hookd_attempts_delivery_number ON ago_hookd_delivery_attempts(delivery_id, attempt_number);

COMMENT ON TABLE ago_hookd_delivery_attempts IS 'History of all webhook delivery attempts';
COMMENT ON COLUMN ago_hookd_delivery_attempts.id IS 'Prefixed nanoID (att_*)';
COMMENT ON COLUMN ago_hookd_delivery_attempts.attempt_number IS '1-based attempt number';
COMMENT ON COLUMN ago_hookd_delivery_attempts.status_code IS 'HTTP status code (0 if network error)';
COMMENT ON COLUMN ago_hookd_delivery_attempts.duration_ms IS 'Request duration in milliseconds';

CREATE TABLE ago_hookd_idempotency_store (
    idempotency_key VARCHAR(255) NOT NULL,
    subscription_id VARCHAR(255) NOT NULL,

    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,

    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    PRIMARY KEY (idempotency_key, subscription_id)
);

CREATE INDEX idx_ago_hookd_idempotency_expires_at ON ago_hookd_idempotency_store(expires_at);
CREATE INDEX idx_ago_hookd_idempotency_subscription_id ON ago_hookd_idempotency_store(subscription_id);

COMMENT ON TABLE ago_hookd_idempotency_store IS 'Tracks idempotency keys for exactly-once delivery';
COMMENT ON COLUMN ago_hookd_idempotency_store.expires_at IS 'Expiration time for automatic cleanup';

CREATE TABLE ago_hookd_circuit_breaker_state (
    endpoint TEXT PRIMARY KEY,

    state VARCHAR(50) NOT NULL DEFAULT 'closed',

    failure_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,

    last_failure TIMESTAMP WITH TIME ZONE,
    opened_at TIMESTAMP WITH TIME ZONE,
    next_retry_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_ago_hookd_circuit_state CHECK (state IN ('closed', 'half_open', 'open')),
    CONSTRAINT chk_ago_hookd_circuit_failures CHECK (failure_count >= 0),
    CONSTRAINT chk_ago_hookd_circuit_successes CHECK (success_count >= 0)
);

CREATE INDEX idx_ago_hookd_circuit_state ON ago_hookd_circuit_breaker_state(state);
CREATE INDEX idx_ago_hookd_circuit_next_retry ON ago_hookd_circuit_breaker_state(next_retry_at)
    WHERE state = 'open';

COMMENT ON TABLE ago_hookd_circuit_breaker_state IS 'Circuit breaker state per webhook endpoint';
COMMENT ON COLUMN ago_hookd_circuit_breaker_state.state IS 'Current circuit state: closed (normal), half_open (testing), open (failing)';
COMMENT ON COLUMN ago_hookd_circuit_breaker_state.failure_count IS 'Consecutive failure count';
COMMENT ON COLUMN ago_hookd_circuit_breaker_state.success_count IS 'Consecutive success count (used in half_open)';


CREATE OR REPLACE FUNCTION ago_hookd_update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_ago_hookd_subscriptions_updated_at
    BEFORE UPDATE ON ago_hookd_subscriptions
    FOR EACH ROW
    EXECUTE FUNCTION ago_hookd_update_updated_at();

CREATE TRIGGER trg_ago_hookd_circuit_breaker_updated_at
    BEFORE UPDATE ON ago_hookd_circuit_breaker_state
    FOR EACH ROW
    EXECUTE FUNCTION ago_hookd_update_updated_at();

CREATE OR REPLACE FUNCTION ago_hookd_cleanup_expired_idempotency()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    DELETE FROM ago_hookd_idempotency_store
    WHERE expires_at < NOW();

    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION ago_hookd_update_updated_at() IS 'Automatically updates updated_at timestamp on row modification';
COMMENT ON FUNCTION ago_hookd_cleanup_expired_idempotency() IS 'Removes expired idempotency keys (call periodically via cron)';

