-- Migration 2026111939:
-- 1. Account-level Stripe EFW/Dispute auto-mitigation toggles (all default false).
-- 2. Dispute original currency & minor-unit amount on payment_cases (preserving NULL for legacy records).
-- 3. Dispute-period entitlement freeze tracking on payment_entitlement_adjustments.
-- 4. Stop-loss full-remaining flag on payment_refund_commands for P1-2/P1-1 coordination.

ALTER TABLE payment_aftercare_policies
    ADD COLUMN IF NOT EXISTS auto_refund BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS auto_cancel_subscription BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS dispute_freeze BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE payment_cases
    ADD COLUMN IF NOT EXISTS original_currency TEXT,
    ADD COLUMN IF NOT EXISTS original_amount_minor BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'payment_cases_original_currency_check'
          AND conrelid = 'payment_cases'::regclass
    ) THEN
        ALTER TABLE payment_cases
            ADD CONSTRAINT payment_cases_original_currency_check
            CHECK (original_currency IS NULL OR original_currency ~ '^[A-Z]{3}$');
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'payment_cases_original_amount_minor_check'
          AND conrelid = 'payment_cases'::regclass
    ) THEN
        ALTER TABLE payment_cases
            ADD CONSTRAINT payment_cases_original_amount_minor_check
            CHECK (original_amount_minor IS NULL OR original_amount_minor >= 0);
    END IF;
END $$;

ALTER TABLE payment_entitlement_adjustments
    ADD COLUMN IF NOT EXISTS freeze_status TEXT NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS freeze_case_id BIGINT REFERENCES payment_cases(id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'payment_entitlement_adjustments_freeze_status_check'
          AND conrelid = 'payment_entitlement_adjustments'::regclass
    ) THEN
        ALTER TABLE payment_entitlement_adjustments
            ADD CONSTRAINT payment_entitlement_adjustments_freeze_status_check
            CHECK (freeze_status IN ('none', 'frozen', 'written_off'));
    END IF;
END $$;

ALTER TABLE payment_refund_commands
    ADD COLUMN IF NOT EXISTS stop_loss_full_remaining BOOLEAN NOT NULL DEFAULT false;

INSERT INTO reader_schema_migrations(version) VALUES (2026111939)
ON CONFLICT DO NOTHING;
