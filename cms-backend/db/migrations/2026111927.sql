-- Scope legacy subscriptions only when the original verified sources agree.
CREATE FUNCTION payment_subscription_environment(s user_subscriptions) RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT CASE WHEN s.checkout_account_id IS NOT NULL THEN (
        SELECT CASE a.environment WHEN 'live' THEN 'production' ELSE a.environment END
        FROM payment_channel_accounts a WHERE a.id=s.checkout_account_id AND a.provider=s.payment_method
    ) WHEN s.payment_method='paypal' THEN (
        SELECT min(environment) FROM (
            SELECT CASE p.identity_environment WHEN 'live' THEN 'production' ELSE p.identity_environment END AS environment
            FROM payment_provider_plans p WHERE p.provider='paypal' AND p.external_plan_id=s.plan_id
              AND p.identity_client_id IS NOT NULL AND p.identity_environment IN ('sandbox','live')
            UNION ALL
            SELECT CASE a.environment WHEN 'live' THEN 'production' ELSE a.environment END
            FROM recharge_orders o JOIN third_party_payment_orders p ON p.order_id=o.id
            JOIN payment_channel_accounts a ON a.id=p.account_id AND a.provider='paypal'
            WHERE o.subscription_id=s.subscription_id AND o.user_id=s.user_id AND p.status='VERIFIED'
        ) evidence HAVING count(DISTINCT environment)=1
    ) END
$$;

-- Prefer the observed receipt scope. Conflicting receipt environments
-- require financial verification before linking to an environment.
CREATE FUNCTION payment_order_environment(o recharge_orders) RETURNS text
LANGUAGE sql STABLE AS $$
    SELECT CASE WHEN count(DISTINCT a.environment)>1 THEN NULL
        ELSE COALESCE(min(a.environment),CASE o.checkout_environment WHEN 'production' THEN 'live' WHEN 'sandbox' THEN 'sandbox' END)
        END
    FROM third_party_payment_orders p JOIN payment_channel_accounts a ON a.id=p.account_id
    WHERE p.order_id=o.id AND p.status IN ('VERIFIED','COMPLETED')
$$;

ALTER TABLE user_subscriptions ADD COLUMN paypal_renewal_failed_at timestamptz;

ALTER TABLE paypal_subscription_intents ADD COLUMN checkout_environment text
    CHECK (checkout_environment IN ('sandbox','production'));
UPDATE paypal_subscription_intents i SET checkout_environment=CASE a.environment WHEN 'live' THEN 'production' ELSE a.environment END
FROM payment_channel_accounts a WHERE a.id=i.checkout_account_id AND a.provider='paypal' AND a.environment IN ('sandbox','live');
ALTER TABLE paypal_subscription_intents DROP CONSTRAINT paypal_subscription_intents_pkey, ADD PRIMARY KEY(intent_id);
CREATE UNIQUE INDEX idx_paypal_vip_pending_user_environment ON paypal_subscription_intents(user_id,checkout_environment) NULLS NOT DISTINCT;
DROP INDEX idx_stripe_vip_pending_user;
CREATE UNIQUE INDEX idx_stripe_vip_pending_user_environment ON payment_stripe_subscription_intents(user_id,checkout_environment)
    WHERE status IN ('creating','buyer_action');

ALTER TABLE payment_checkout_attempts ADD COLUMN paypal_switch_token uuid;
-- The previous switch path set lookup-only before the first capture.
UPDATE payment_checkout_attempts SET paypal_switch_token=gen_random_uuid(),capture_lookup_only=false
WHERE provider='paypal' AND status='buyer_action' AND capture_first_sent_at IS NULL AND capture_lookup_only;

ALTER TABLE payment_deferred_fulfillment DROP CONSTRAINT payment_deferred_fulfillment_pkey,
    ADD PRIMARY KEY(order_id,receipt_id);

CREATE OR REPLACE VIEW payment_principal_loss_by_receipt AS
WITH movements AS (
    SELECT x.receipt_id,m.id,m.kind,m.provider_code,a.provider,v.amount_minor,
        (
            SELECT CASE WHEN count(*)=1 THEN min(c.outcome) END FROM payment_cases c
            WHERE c.account_id=m.account_id AND c.kind='dispute' AND NOT c.is_open
            AND ((a.provider='stripe' AND c.outcome IN ('lost','won') AND EXISTS (
                SELECT 1 FROM payment_ledger_references r WHERE r.movement_id=m.id
                AND r.object_type='stripe_dispute' AND r.object_id=c.external_id
            )) OR (a.provider='paypal' AND c.outcome IN ('RESOLVED_BUYER_FAVOUR','RESOLVED_SELLER_FAVOUR','ACCEPTED')
                AND c.stage IN ('CHARGEBACK','PRE_ARBITRATION','ARBITRATION')
                AND EXISTS (SELECT 1 FROM payment_case_receipts cr WHERE cr.case_id=c.id AND cr.receipt_id=x.receipt_id)
                AND (SELECT count(*) FROM payment_case_receipts other JOIN payment_cases d ON d.id=other.case_id
                    WHERE other.receipt_id=x.receipt_id AND d.kind='dispute')=1))
        ) AS verdict
    FROM payment_ledger_receipt_matches x JOIN payment_ledger_movements m ON m.id=x.movement_id
    JOIN payment_channel_accounts a ON a.id=m.account_id
    JOIN payment_ledger_amounts v ON v.movement_id=m.id AND v.component='gross' AND v.currency='USD'
    WHERE m.currency='USD' AND m.kind IN ('dispute_debit','dispute_credit','reversal')
      AND NOT EXISTS (
          SELECT 1 FROM payment_ledger_references r JOIN third_party_payment_orders t ON t.id=x.receipt_id
          WHERE r.movement_id=m.id AND (
            (r.object_type='paypal_refund' AND EXISTS(SELECT 1 FROM paypal_refund_items f
                WHERE f.refund_id=r.object_id AND f.capture_id=t.capture_id AND f.currency=t.currency))
            OR (r.object_type='stripe_refund' AND EXISTS(SELECT 1 FROM payment_stripe_refund_items f
                WHERE f.refund_ref=r.object_id AND f.receipt_id=t.id))
          )
      )
), eligible AS (
    SELECT *, (provider='paypal' AND kind='reversal' AND provider_code='T1106') AS independent
    FROM movements
    WHERE (verdict IS NOT NULL AND kind IN ('dispute_debit','dispute_credit'))
       OR (provider='paypal' AND kind='reversal' AND provider_code='T1106')
)
SELECT receipt_id,greatest(0,-sum(amount_minor)) AS loss_minor,
    greatest(0,-sum(amount_minor) FILTER (WHERE independent OR verdict IN ('lost','RESOLVED_BUYER_FAVOUR','ACCEPTED')))
        AS confirmed_loss_minor,
    md5(string_agg(id::text||':'||amount_minor::text||':'||coalesce(verdict,''),',' ORDER BY id)) AS evidence_key
FROM eligible GROUP BY receipt_id;

CREATE OR REPLACE VIEW payment_principal_loss AS
WITH funding AS (
    SELECT f.order_id,f.receipt_id,f.principal_minor,principal.evidence_key,COALESCE(adj.blocked,false) AS blocked,
        CASE WHEN t.payment_provider='stripe' THEN greatest(0,t.gross_amount*100-f.principal_minor) ELSE 0 END AS nonprincipal,
        COALESCE(principal.loss_minor,0)+COALESCE(refund.amount_minor,0) AS loss_minor,
        COALESCE(principal.confirmed_loss_minor,0)+COALESCE(refund.amount_minor,0) AS confirmed_minor,
        COALESCE(refund.evidence,'') AS refund_evidence
    FROM payment_fulfillment_funding f JOIN third_party_payment_orders t ON t.id=f.receipt_id
    LEFT JOIN LATERAL (
        SELECT sum(amount_minor) AS amount_minor, string_agg(ref||':'||amount_minor,',' ORDER BY ref) AS evidence
        FROM (
            SELECT refund_id AS ref,amount_cents::bigint AS amount_minor FROM paypal_refund_items
            WHERE t.payment_provider='paypal' AND capture_id=t.capture_id AND currency=t.currency
            UNION ALL
            SELECT refund_ref,amount_minor FROM payment_stripe_refund_items WHERE t.payment_provider='stripe' AND receipt_id=t.id
        ) items
    ) refund ON true
    LEFT JOIN payment_principal_loss_by_receipt principal ON f.receipt_id=principal.receipt_id
    LEFT JOIN payment_entitlement_adjustments adj ON adj.order_id=f.order_id WHERE f.currency='USD'
)
SELECT order_id,
    sum(CASE WHEN principal_minor IS NOT NULL THEN least(principal_minor,greatest(0,loss_minor-nonprincipal)) END) AS loss_minor,
    md5(string_agg(receipt_id::text||':'||coalesce(principal_minor::text,'unknown')||':'||coalesce(evidence_key,'')||':'||refund_evidence,
        ',' ORDER BY receipt_id)) AS evidence_key,
    CASE WHEN bool_and(principal_minor IS NOT NULL) THEN
        sum(least(principal_minor,greatest(0,(CASE WHEN blocked THEN loss_minor ELSE confirmed_minor END)-nonprincipal)))>=sum(principal_minor)
    ELSE bool_or(blocked) END AS target_blocked
FROM funding GROUP BY order_id HAVING bool_or(evidence_key IS NOT NULL);

INSERT INTO reader_schema_migrations(version) VALUES(2026111927);
