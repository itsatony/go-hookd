-- Migration: 000002_inline_deliveries (ROLLBACK)
-- Description: Revert inline deliveries changes
-- Author: go-hookd
-- Created: 2025-12-10

-- =============================================================================
-- ROLLBACK DELIVERIES TABLE CHANGES
-- =============================================================================

-- Step 1: Drop the check constraint
ALTER TABLE deliveries DROP CONSTRAINT IF EXISTS chk_delivery_target;

-- Step 2: Drop the inline deliveries index
DROP INDEX IF EXISTS idx_deliveries_inline;

-- Step 3: Remove inline delivery columns
ALTER TABLE deliveries DROP COLUMN IF EXISTS url;
ALTER TABLE deliveries DROP COLUMN IF EXISTS secret;

-- Step 4: Remove any rows with NULL subscription_id (cannot be restored)
DELETE FROM deliveries WHERE subscription_id IS NULL;

-- Step 5: Make subscription_id NOT NULL again
ALTER TABLE deliveries ALTER COLUMN subscription_id SET NOT NULL;

-- Step 6: Re-add foreign key constraint
ALTER TABLE deliveries ADD CONSTRAINT fk_deliveries_subscription
    FOREIGN KEY (subscription_id) REFERENCES subscriptions(id) ON DELETE CASCADE;

-- =============================================================================
-- ROLLBACK COMPLETE
-- =============================================================================
