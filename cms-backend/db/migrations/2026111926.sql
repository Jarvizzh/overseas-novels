ALTER TABLE payment_stripe_subscription_intents
    ADD COLUMN create_lookup_only boolean NOT NULL DEFAULT false,
    ADD COLUMN cancel_lookup_only boolean NOT NULL DEFAULT false;

CREATE TABLE payment_event_recovery_failures (
    account_id bigint NOT NULL REFERENCES payment_channel_accounts(id),
    event_id text NOT NULL,
    event jsonb NOT NULL,
    last_error text NOT NULL,
    failed_at timestamptz NOT NULL DEFAULT now(),
    next_attempt_at timestamptz,
    lease_token uuid,
    lease_until timestamptz,
    resolved_at timestamptz,
    PRIMARY KEY (account_id,event_id)
);
CREATE INDEX idx_payment_recovery_failure_retry ON payment_event_recovery_failures(account_id,next_attempt_at)
    WHERE resolved_at IS NULL AND next_attempt_at IS NOT NULL;

ALTER TABLE payment_fulfillment_funding
    ALTER COLUMN principal_minor DROP NOT NULL,
    DROP CONSTRAINT payment_fulfillment_funding_principal_minor_check,
    ADD CHECK (principal_minor >= 0);

-- Older Managed Payments funding used the inclusive charge. Keep the verified
-- charge and allocation unchanged while the itemized pre-tax amount is re-read.
UPDATE payment_fulfillment_funding f SET principal_minor=NULL
WHERE EXISTS (SELECT 1 FROM payment_checkout_attempts a JOIN third_party_payment_orders t
    ON t.order_id=a.order_id AND t.account_id=a.account_id AND t.external_order_id=a.provider_order_id
    WHERE t.id=f.receipt_id AND a.provider='stripe' AND a.sales_mode='merchant_of_record')
OR EXISTS (SELECT 1 FROM payment_stripe_invoice_periods p JOIN payment_stripe_subscription_intents v ON v.id=p.intent_id
    WHERE p.order_id=f.order_id AND v.sales_mode='merchant_of_record');

CREATE OR REPLACE VIEW payment_principal_loss AS
WITH funding AS (
    SELECT f.order_id,f.receipt_id,f.principal_minor,principal.evidence_key,COALESCE(adj.blocked,false) AS blocked,
        CASE WHEN t.payment_provider='stripe' THEN greatest(0,t.gross_amount*100-f.principal_minor) ELSE 0 END AS nonprincipal,
        COALESCE(principal.loss_minor,0) AS loss_minor,COALESCE(principal.confirmed_loss_minor,0) AS confirmed_minor
    FROM payment_fulfillment_funding f JOIN third_party_payment_orders t ON t.id=f.receipt_id
    LEFT JOIN payment_principal_loss_by_receipt principal ON f.receipt_id=principal.receipt_id
    LEFT JOIN payment_entitlement_adjustments adj ON adj.order_id=f.order_id WHERE f.currency='USD'
)
SELECT order_id,
    sum(CASE WHEN principal_minor IS NOT NULL THEN least(principal_minor,greatest(0,loss_minor-nonprincipal)) END) AS loss_minor,
    md5(string_agg(receipt_id::text||':'||coalesce(principal_minor::text,'unknown')||':'||coalesce(evidence_key,''),
        ',' ORDER BY receipt_id)) AS evidence_key,
    CASE WHEN bool_and(principal_minor IS NOT NULL) THEN
        sum(least(principal_minor,greatest(0,(CASE WHEN blocked THEN loss_minor ELSE confirmed_minor END)-nonprincipal)))>=sum(principal_minor)
    ELSE bool_or(blocked) END AS target_blocked
FROM funding GROUP BY order_id HAVING bool_or(evidence_key IS NOT NULL);

INSERT INTO reader_schema_migrations(version) VALUES(2026111926);
