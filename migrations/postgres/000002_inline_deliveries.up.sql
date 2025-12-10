-- Migration: 000002_inline_deliveries
-- Description: Enable inline deliveries without subscription (nullable subscription_id)
-- Author: go-hookd
-- Created: 2025-12-10

-- =============================================================================
-- DELIVERIES TABLE MODIFICATION
-- =============================================================================
-- Make subscription_id nullable to support inline deliveries
-- (deliveries that don't require a pre-created subscription)

-- Step 1: Drop the existing foreign key constraint
ALTER TABLE deliveries DROP CONSTRAINT IF EXISTS fk_deliveries_subscription;

-- Step 2: Alter the column to allow NULL values
ALTER TABLE deliveries ALTER COLUMN subscription_id DROP NOT NULL;

-- Step 3: Add inline delivery columns
-- URL for direct webhook delivery (only used for inline deliveries)
ALTER TABLE deliveries ADD COLUMN IF NOT EXISTS url TEXT;

-- Secret for inline delivery signature (only used for inline deliveries)
ALTER TABLE deliveries ADD COLUMN IF NOT EXISTS secret VARCHAR(512);

-- Step 4: Add a check constraint to ensure either subscription_id OR url is present
ALTER TABLE deliveries ADD CONSTRAINT chk_delivery_target
    CHECK (subscription_id IS NOT NULL OR url IS NOT NULL);

-- Step 5: Add index for inline deliveries (deliveries without subscription)
CREATE INDEX IF NOT EXISTS idx_deliveries_inline ON deliveries(tenant_id, event_type)
    WHERE subscription_id IS NULL;

-- =============================================================================
-- COMMENTS
-- =============================================================================
COMMENT ON COLUMN deliveries.url IS 'Direct URL for inline deliveries (NULL for subscription-based)';
COMMENT ON COLUMN deliveries.secret IS 'HMAC secret for inline deliveries (NULL for subscription-based)';
COMMENT ON CONSTRAINT chk_delivery_target ON deliveries IS 'Ensures delivery has either subscription_id OR url for inline delivery';
