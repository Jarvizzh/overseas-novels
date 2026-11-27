-- P1-3: Persisted Stripe Radar review approval commands deduplicated by (account_id, environment, review_id).
CREATE TABLE IF NOT EXISTS payment_stripe_review_approvals (
    id UUID PRIMARY KEY,
    case_id BIGINT NOT NULL REFERENCES payment_cases(id),
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    environment TEXT NOT NULL CHECK (environment IN ('sandbox','live')),
    review_id TEXT NOT NULL CHECK (btrim(review_id)<>''),
    idempotency_key TEXT NOT NULL CHECK (btrim(idempotency_key)<>''),
    requested_by TEXT NOT NULL REFERENCES admins(id),
    reason TEXT NOT NULL CHECK (btrim(reason)<>''),
    status TEXT NOT NULL DEFAULT 'pending_confirmation' CHECK (status IN ('pending_confirmation','confirmed','failed')),
    closed_reason TEXT NOT NULL DEFAULT '',
    lookup_only BOOLEAN NOT NULL DEFAULT false,
    first_sent_at TIMESTAMPTZ,
    retry_until TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ,
    UNIQUE (account_id, environment, review_id)
);

CREATE INDEX IF NOT EXISTS idx_stripe_review_approvals_due
    ON payment_stripe_review_approvals(account_id, next_attempt_at)
    WHERE status = 'pending_confirmation';

CREATE INDEX IF NOT EXISTS idx_stripe_review_approvals_case
    ON payment_stripe_review_approvals(case_id);

-- P2-3: Per-group Stripe Events API recovery cursors (<= 20 event types per group).
CREATE TABLE IF NOT EXISTS payment_event_recovery_groups (
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    group_key TEXT NOT NULL CHECK (btrim(group_key)<>''),
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL CHECK (window_end > window_start),
    page_cursor TEXT NOT NULL DEFAULT '',
    last_complete_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    last_error TEXT,
    PRIMARY KEY (account_id, group_key)
);

CREATE INDEX IF NOT EXISTS idx_payment_event_recovery_groups_due
    ON payment_event_recovery_groups(account_id, next_attempt_at);

INSERT INTO reader_schema_migrations(version) VALUES (2026111940)
ON CONFLICT (version) DO NOTHING;
