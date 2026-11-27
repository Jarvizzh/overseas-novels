-- Add persistent Stripe Report Run scheduling state and explicit MoR tax/fee columns
-- on settlement rows while preserving full backward compatibility with manual imports.

ALTER TABLE payment_settlement_rows
    ADD COLUMN IF NOT EXISTS withheld_tax_minor BIGINT,
    ADD COLUMN IF NOT EXISTS fee_net_of_withheld_tax_minor BIGINT;

CREATE TABLE IF NOT EXISTS payment_stripe_report_runs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    environment TEXT NOT NULL CHECK (environment IN ('sandbox','live')),
    report_type TEXT NOT NULL CHECK (btrim(report_type) <> ''),
    schedule_key TEXT NOT NULL CHECK (btrim(schedule_key) <> ''),
    params_digest TEXT NOT NULL CHECK (btrim(params_digest) <> ''),
    parameters JSONB NOT NULL DEFAULT '{}'::jsonb,
    include_mor_columns BOOLEAN NOT NULL DEFAULT false,
    expected_columns TEXT[] NOT NULL DEFAULT '{}',
    idempotency_key TEXT NOT NULL CHECK (btrim(idempotency_key) <> ''),
    report_run_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','creating','running','succeeded','enqueued','failed')),
    import_command_id UUID REFERENCES payment_settlement_commands(id),
    actor_id TEXT NOT NULL REFERENCES admins(id),
    reason TEXT NOT NULL CHECK (btrim(reason) <> ''),
    last_error TEXT,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (schedule_key),
    UNIQUE (account_id, environment, report_type, params_digest)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_stripe_report_runs_run_id
    ON payment_stripe_report_runs(account_id, environment, report_run_id)
    WHERE report_run_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_payment_stripe_report_runs_due
    ON payment_stripe_report_runs(environment, next_attempt_at)
    WHERE status IN ('pending','creating','running','succeeded');

INSERT INTO reader_schema_migrations(version) VALUES (2026111941)
ON CONFLICT (version) DO NOTHING;
