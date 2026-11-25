-- Settlement reports, channel transfers and independent bank credits are
-- separate records. Report rows refer to the existing authoritative ledger.
ALTER TABLE payment_ledger_evidence DROP CONSTRAINT payment_ledger_evidence_source_check;
ALTER TABLE payment_ledger_evidence ADD CONSTRAINT payment_ledger_evidence_source_check
    CHECK (source IN ('paypal_transaction_search','paypal_settlement','stripe_balance_transaction',
        'stripe_report','stripe_webhook','stripe_refund_lookup'));

CREATE TABLE payment_settlement_imports (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    source TEXT NOT NULL CHECK (source IN ('paypal_settlement','stripe_report','stripe_payout')),
    external_ref TEXT NOT NULL,
    fingerprint BYTEA NOT NULL CHECK (octet_length(fingerprint)=32),
    operator TEXT NOT NULL CHECK (btrim(operator)<>''),
    reason TEXT NOT NULL CHECK (btrim(reason)<>''),
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (account_id, source, external_ref, fingerprint)
);

CREATE TABLE payment_settlement_rows (
    import_id BIGINT NOT NULL REFERENCES payment_settlement_imports(id),
    source_file TEXT NOT NULL,
    row_number INTEGER NOT NULL CHECK (row_number>0),
    movement_id BIGINT REFERENCES payment_ledger_movements(id),
    payload JSONB NOT NULL,
    review_reason TEXT,
    PRIMARY KEY (import_id, source_file, row_number),
    CHECK (movement_id IS NOT NULL OR review_reason IS NOT NULL)
);

CREATE INDEX idx_settlement_rows_movement ON payment_settlement_rows(movement_id) WHERE movement_id IS NOT NULL;

CREATE TABLE payment_settlement_payouts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    external_ref TEXT NOT NULL,
    currency VARCHAR(3) NOT NULL CHECK (currency='USD'),
    expected_minor BIGINT NOT NULL CHECK (expected_minor>0),
    bank_reference TEXT,
    expected_at TIMESTAMPTZ,
    channel_status TEXT NOT NULL CHECK (channel_status IN ('pending','in_transit','paid','failed','canceled')),
    allocation_required BOOLEAN NOT NULL,
    allocation_complete BOOLEAN NOT NULL,
    transfer_key TEXT,
    reversal_key TEXT,
    last_observed_at TIMESTAMPTZ NOT NULL,
    review_required BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (account_id, external_ref),
    FOREIGN KEY (account_id, transfer_key) REFERENCES payment_ledger_movements(account_id, movement_key),
    FOREIGN KEY (account_id, reversal_key) REFERENCES payment_ledger_movements(account_id, movement_key)
);

CREATE TABLE payment_settlement_payout_evidence (
    payout_id BIGINT NOT NULL REFERENCES payment_settlement_payouts(id),
    import_id BIGINT NOT NULL REFERENCES payment_settlement_imports(id),
    payload JSONB NOT NULL,
    PRIMARY KEY (payout_id, import_id)
);

CREATE TABLE payment_bank_statement_entries (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    environment TEXT NOT NULL CHECK (environment IN ('sandbox','live')),
    bank_account TEXT NOT NULL CHECK (btrim(bank_account)<>''),
    transaction_id TEXT NOT NULL CHECK (btrim(transaction_id)<>''),
    statement_ref TEXT NOT NULL CHECK (btrim(statement_ref)<>''),
    booked_at TIMESTAMPTZ NOT NULL,
    currency VARCHAR(3) NOT NULL CHECK (currency='USD'),
    amount_minor BIGINT NOT NULL CHECK (amount_minor<>0),
    bank_reference TEXT NOT NULL,
    operator TEXT NOT NULL CHECK (btrim(operator)<>''),
    reason TEXT NOT NULL CHECK (btrim(reason)<>''),
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (environment, bank_account, transaction_id)
);

CREATE TABLE payment_settlement_bank_reviews (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    payout_id BIGINT NOT NULL REFERENCES payment_settlement_payouts(id),
    entry_id BIGINT NOT NULL REFERENCES payment_bank_statement_entries(id),
    outcome TEXT NOT NULL CHECK (outcome IN ('matched','amount_mismatch','reference_mismatch','not_ready')),
    operator TEXT NOT NULL CHECK (btrim(operator)<>''),
    reason TEXT NOT NULL CHECK (btrim(reason)<>''),
    manual_evidence TEXT,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (payout_id, entry_id, outcome)
);

CREATE UNIQUE INDEX idx_settlement_matched_payout ON payment_settlement_bank_reviews(payout_id) WHERE outcome='matched';
CREATE UNIQUE INDEX idx_settlement_matched_bank_entry ON payment_settlement_bank_reviews(entry_id) WHERE outcome='matched';

CREATE VIEW payment_settlement_payout_checks AS
SELECT p.id, p.account_id, p.external_ref, p.channel_status, p.currency, p.expected_minor,
    p.expected_at, p.bank_reference, p.allocation_required, p.allocation_complete,
    members.row_count, members.net_total_minor, debit.amount_minor AS transfer_minor,
    CASE
        WHEN p.review_required OR transfer.review_required OR members.has_conflict THEN 'review'
        WHEN p.channel_status IN ('failed','canceled') OR p.reversal_key IS NOT NULL OR (
            a.provider='paypal' AND EXISTS (
                SELECT 1 FROM payment_ledger_references r
                JOIN payment_ledger_movements reversal ON reversal.id=r.movement_id AND reversal.account_id=p.account_id
                JOIN payment_ledger_references original ON original.movement_id=transfer.id
                    AND original.object_type='paypal_payout_transfer' AND original.object_id=r.object_id
                WHERE r.object_type='paypal_payout' AND reversal.provider_code='T1101'
                    AND reversal.currency=p.currency AND reversal.posted_at IS NOT NULL
            )
        ) THEN 'reversed'
        WHEN p.channel_status<>'paid' THEN 'awaiting_channel'
        WHEN debit.amount_minor IS NULL OR transfer.posted_at IS NULL THEN 'missing_transfer'
        WHEN transfer.kind<>'payout' THEN 'review'
        WHEN debit.amount_minor::numeric<>-p.expected_minor::numeric THEN 'transfer_amount_mismatch'
        WHEN p.allocation_required AND (NOT p.allocation_complete OR members.row_count=0 OR members.incomplete) THEN 'missing_movements'
        WHEN p.allocation_required AND members.net_total_minor<>p.expected_minor THEN 'allocation_amount_mismatch'
        ELSE 'ready'
    END AS channel_check
FROM payment_settlement_payouts p
JOIN payment_channel_accounts a ON a.id=p.account_id
LEFT JOIN payment_ledger_movements transfer ON transfer.account_id=p.account_id AND transfer.movement_key=p.transfer_key
LEFT JOIN payment_ledger_amounts debit ON debit.movement_id=transfer.id AND debit.component='gross' AND debit.currency=p.currency
LEFT JOIN LATERAL (
    SELECT count(*) AS row_count, sum(net.amount_minor) AS net_total_minor,
        COALESCE(bool_or(net.amount_minor IS NULL OR m.posted_at IS NULL),false) AS incomplete,
        COALESCE(bool_or(m.review_required OR m.currency<>p.currency OR EXISTS (
            SELECT 1 FROM payment_ledger_references another
            WHERE another.movement_id=m.id AND another.object_type IN ('stripe_payout','stripe_payout_allocation')
                AND another.object_id<>p.external_ref
        )),false) AS has_conflict
    FROM payment_ledger_references r
    JOIN payment_ledger_movements m ON m.id=r.movement_id AND m.account_id=p.account_id
        AND m.movement_key IS DISTINCT FROM p.transfer_key
        AND m.movement_key IS DISTINCT FROM p.reversal_key
    LEFT JOIN payment_ledger_amounts net ON net.movement_id=m.id AND net.component='net' AND net.currency=p.currency
    WHERE a.provider='stripe' AND r.object_type='stripe_payout_allocation' AND r.object_id=p.external_ref
) members ON true;

CREATE VIEW payment_settlement_reconciliation AS
SELECT p.*, bank.amount_minor AS bank_amount_minor, bank.currency AS bank_currency,
    bank.booked_at, bank.transaction_id AS bank_transaction_id,
    bank.amount_minor::numeric-p.expected_minor AS difference_minor,
    CASE
        WHEN matched.id IS NOT NULL AND (p.channel_check<>'ready'
            OR (p.bank_reference IS NOT NULL AND bank.bank_reference<>p.bank_reference)) THEN 'matched_requires_review'
        WHEN matched.id IS NOT NULL THEN 'matched'
        WHEN p.channel_check<>'ready' THEN p.channel_check
        WHEN candidate.outcome IN ('amount_mismatch','reference_mismatch') THEN candidate.outcome
        WHEN p.expected_at IS NOT NULL AND p.expected_at<date_trunc('day',now(),'UTC') THEN 'bank_delayed'
        ELSE 'awaiting_bank'
    END AS status
FROM payment_settlement_payout_checks p
LEFT JOIN payment_settlement_bank_reviews matched ON matched.payout_id=p.id AND matched.outcome='matched'
LEFT JOIN LATERAL (
    SELECT entry_id,outcome FROM payment_settlement_bank_reviews r WHERE r.payout_id=p.id ORDER BY id DESC LIMIT 1
) candidate ON true
LEFT JOIN payment_bank_statement_entries bank ON bank.id=COALESCE(matched.entry_id,candidate.entry_id);

INSERT INTO reader_schema_migrations (version) VALUES (2026111920);
