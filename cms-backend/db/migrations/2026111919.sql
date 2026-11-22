CREATE TABLE payment_ledger_movements (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    movement_key TEXT NOT NULL CHECK (btrim(movement_key) <> ''),
    kind TEXT CHECK (kind IN ('payment','refund','reversal','dispute_debit','dispute_credit',
        'fee','tax','fx','payout','hold','release','other')),
    provider_code TEXT,
    currency VARCHAR(3) CHECK (currency ~ '^[A-Z]{3}$'),
    exchange_rate NUMERIC CHECK (exchange_rate > 0 AND exchange_rate < 'Infinity'::numeric),
    occurred_at TIMESTAMPTZ,
    posted_at TIMESTAMPTZ,
    review_required BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (account_id, movement_key),
    CHECK ((posted_at IS NULL) = (kind IS NULL) AND (posted_at IS NULL) = (currency IS NULL))
);

CREATE TABLE payment_ledger_evidence (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    movement_id BIGINT NOT NULL REFERENCES payment_ledger_movements(id),
    source TEXT NOT NULL CHECK (source IN ('paypal_transaction_search','stripe_balance_transaction',
        'stripe_report','stripe_webhook','stripe_refund_lookup')),
    source_ref TEXT NOT NULL CHECK (btrim(source_ref) <> ''),
    payload_hash BYTEA NOT NULL CHECK (octet_length(payload_hash) = 32),
    payload JSONB NOT NULL,
    conflicts BOOLEAN NOT NULL DEFAULT false,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (movement_id, id),
    UNIQUE (movement_id, source, source_ref, payload_hash)
);

CREATE TABLE payment_ledger_amounts (
    movement_id BIGINT NOT NULL REFERENCES payment_ledger_movements(id),
    component TEXT NOT NULL CHECK (component IN ('gross','fee','net','sales_tax','withheld_tax',
        'processing_fee','customer_facing','fx_fee')),
    currency VARCHAR(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    amount_minor BIGINT NOT NULL,
    evidence_id BIGINT NOT NULL,
    PRIMARY KEY (movement_id, component),
    FOREIGN KEY (movement_id, evidence_id) REFERENCES payment_ledger_evidence(movement_id, id)
);

CREATE TABLE payment_ledger_references (
    movement_id BIGINT NOT NULL REFERENCES payment_ledger_movements(id),
    object_type TEXT NOT NULL CHECK (btrim(object_type) <> ''),
    object_id TEXT NOT NULL CHECK (btrim(object_id) <> ''),
    PRIMARY KEY (movement_id, object_type, object_id)
);

CREATE INDEX idx_payment_ledger_reference_lookup ON payment_ledger_references(object_type, object_id, movement_id);

-- An exact receipt reference must resolve to one verified receipt in the
-- same account. Resolve at read time so ledger-before-receipt works as well.
CREATE VIEW payment_ledger_receipt_matches AS
SELECT m.id AS movement_id, p.id AS receipt_id
FROM payment_ledger_movements m
JOIN payment_ledger_references r ON r.movement_id=m.id
JOIN third_party_payment_orders p ON p.account_id=m.account_id AND p.capture_id=r.object_id
    AND p.status='VERIFIED'
    AND ((r.object_type='stripe_charge' AND p.payment_provider='stripe' AND p.object_type='charge')
        OR (r.object_type='paypal_transaction' AND p.payment_provider='paypal' AND p.object_type IN ('capture','sale')))
WHERE m.posted_at IS NOT NULL AND NOT m.review_required
    AND NOT EXISTS (
        SELECT 1 FROM payment_ledger_references other
        WHERE other.movement_id=m.id AND other.object_type=r.object_type AND other.object_id<>r.object_id
    )
    AND NOT EXISTS (
        SELECT 1 FROM third_party_payment_orders other
        WHERE other.account_id=p.account_id AND other.payment_provider=p.payment_provider
            AND other.capture_id=p.capture_id AND other.id<>p.id AND other.status='VERIFIED'
            AND ((r.object_type='stripe_charge' AND other.object_type='charge')
                OR (r.object_type='paypal_transaction' AND other.object_type IN ('capture','sale')))
    );

-- Legacy PayPal receipts used the gross total as a fallback for net.
UPDATE third_party_payment_orders p
SET fee_amount=NULL, net_amount=NULL
FROM recharge_orders o
WHERE o.id=p.order_id AND p.payment_provider='paypal'
    AND p.net_amount IS NOT DISTINCT FROM p.gross_amount
    AND COALESCE(p.fee_amount,0)=0
    AND (o.order_type='subscription'
        OR p.raw_payload #>> '{purchase_units,0,payments,captures,0,seller_receivable_breakdown,net_amount,value}' IS NULL);

ALTER TABLE third_party_payment_orders
    ALTER COLUMN fee_amount DROP DEFAULT,
    ALTER COLUMN net_amount DROP DEFAULT;

INSERT INTO reader_schema_migrations (version) VALUES (2026111919);
