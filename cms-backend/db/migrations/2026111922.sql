CREATE TABLE payment_aftercare_policies (
    account_id BIGINT PRIMARY KEY REFERENCES payment_channel_accounts(id),
    dispute_lookup BOOLEAN NOT NULL DEFAULT false,
    hold_fulfillment BOOLEAN NOT NULL DEFAULT false,
    reviewer_id TEXT REFERENCES admins(id),
    finance_id TEXT REFERENCES admins(id),
    review_seconds INTEGER CHECK (review_seconds > 0),
    refund_seconds INTEGER CHECK (refund_seconds > 0),
    reminder_seconds INTEGER CHECK (reminder_seconds > 0),
    CHECK (NOT hold_fulfillment OR (reviewer_id IS NOT NULL AND finance_id IS NOT NULL
        AND review_seconds IS NOT NULL AND refund_seconds IS NOT NULL))
);

CREATE TABLE payment_cases (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    kind TEXT NOT NULL CHECK (kind IN ('risk_signal','risk_review','dispute','reversal')),
    external_id TEXT NOT NULL CHECK (btrim(external_id)<>''),
    receipt_refs TEXT[] NOT NULL DEFAULT '{}',
    order_ref TEXT,
    provider_status TEXT NOT NULL,
    stage TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    is_open BOOLEAN NOT NULL,
    due_at TIMESTAMPTZ,
    amount_minor BIGINT CHECK (amount_minor >= 0),
    sales_mode TEXT CHECK (sales_mode IN ('direct','mor')),
    observed_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    owner_id TEXT REFERENCES admins(id),
    work_state TEXT NOT NULL DEFAULT 'observed' CHECK (work_state IN ('observed','pending','approved','refund_due','dispute_due','closed')),
    work_due_at TIMESTAMPTZ,
    escalated_at TIMESTAMPTZ,
    UNIQUE (account_id,kind,external_id)
);
CREATE INDEX idx_payment_cases_receipts ON payment_cases USING gin(receipt_refs);
CREATE INDEX idx_payment_cases_work ON payment_cases(work_due_at,id) WHERE work_state='pending';
CREATE INDEX idx_payment_cases_dispute_due ON payment_cases(due_at,id) WHERE kind='dispute' AND is_open;
CREATE INDEX idx_payment_cases_account_kind ON payment_cases(account_id,kind,id DESC);

CREATE TABLE payment_case_evidence (
    case_id BIGINT NOT NULL REFERENCES payment_cases(id),
    source TEXT NOT NULL CHECK (source IN ('webhook','provider_api','reconciliation')),
    source_ref TEXT NOT NULL CHECK (btrim(source_ref)<>''),
    digest BYTEA NOT NULL CHECK (octet_length(digest)=32),
    observed_at TIMESTAMPTZ NOT NULL,
    evidence JSONB NOT NULL,
    PRIMARY KEY(case_id,source,source_ref,digest)
);

CREATE TABLE payment_case_audit (
    case_id BIGINT NOT NULL REFERENCES payment_cases(id),
    request_id UUID NOT NULL,
    actor_id TEXT REFERENCES admins(id),
    action TEXT NOT NULL,
    reason TEXT NOT NULL CHECK (btrim(reason)<>''),
    parameters JSONB NOT NULL DEFAULT '{}',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (case_id,request_id)
);

CREATE TABLE payment_case_reminders (
    case_id BIGINT NOT NULL REFERENCES payment_cases(id),
    due_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(case_id,due_at)
);

CREATE VIEW payment_case_receipts AS
SELECT c.id AS case_id,t.id AS receipt_id,t.order_id,t.capture_id
FROM payment_cases c
JOIN payment_channel_accounts a ON a.id=c.account_id
JOIN third_party_payment_orders t ON t.account_id=c.account_id AND t.payment_provider=a.provider
    AND c.receipt_refs @> ARRAY[t.capture_id::text] AND t.status='VERIFIED' AND t.currency='USD'
WHERE NOT EXISTS (
    SELECT 1 FROM third_party_payment_orders duplicate
    WHERE duplicate.account_id=t.account_id AND duplicate.payment_provider=t.payment_provider
        AND duplicate.capture_id=t.capture_id AND duplicate.status='VERIFIED' AND duplicate.id<>t.id
);

CREATE TABLE payment_deferred_fulfillment (
    order_id BIGINT PRIMARY KEY REFERENCES recharge_orders(id),
    receipt_id BIGINT NOT NULL UNIQUE REFERENCES third_party_payment_orders(id),
    case_id BIGINT REFERENCES payment_cases(id),
    deferred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);
CREATE INDEX idx_payment_deferred_due ON payment_deferred_fulfillment(next_check_at) WHERE resolved_at IS NULL;

CREATE TABLE payment_entitlement_adjustments (
    order_id BIGINT PRIMARY KEY REFERENCES recharge_orders(id),
    blocked BOOLEAN NOT NULL,
    charged_coins INT NOT NULL CHECK (charged_coins >= 0),
    bonus_coins INT NOT NULL CHECK (bonus_coins >= 0),
    policy_evidence TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- One authoritative gross amount per movement. Cases only classify whether a
-- debit has become a final principal loss. Holds, fees and ambiguous receipts
-- are visible in the ledger but cannot independently revoke an entitlement.
CREATE VIEW payment_principal_loss_by_receipt AS
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

CREATE VIEW payment_principal_loss AS
SELECT f.order_id,sum(least(f.principal_minor,COALESCE(principal.loss_minor,0))) AS loss_minor,
    md5(string_agg(f.receipt_id::text||':'||f.principal_minor::text||':'||COALESCE(principal.evidence_key,''),
        ',' ORDER BY f.receipt_id)) AS evidence_key,
    sum(least(f.principal_minor,CASE WHEN COALESCE(adj.blocked,false) THEN COALESCE(principal.loss_minor,0)
        ELSE COALESCE(principal.confirmed_loss_minor,0) END))>=sum(f.principal_minor) AS target_blocked
FROM payment_fulfillment_funding f LEFT JOIN payment_principal_loss_by_receipt principal ON f.receipt_id=principal.receipt_id
LEFT JOIN payment_entitlement_adjustments adj ON adj.order_id=f.order_id
WHERE f.currency='USD'
GROUP BY f.order_id
HAVING bool_or(principal.evidence_key IS NOT NULL);

CREATE TABLE payment_loss_commands (
    id UUID PRIMARY KEY,
    case_id BIGINT NOT NULL REFERENCES payment_cases(id),
    order_id BIGINT NOT NULL REFERENCES recharge_orders(id),
    requested_by TEXT NOT NULL REFERENCES admins(id),
    blocked BOOLEAN NOT NULL,
    evidence_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','applied','stale')),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    finished_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_payment_loss_pending ON payment_loss_commands(order_id) WHERE status='pending';

CREATE OR REPLACE FUNCTION complete_paypal_refund(target_order BIGINT, provider_refund TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE
    purchase recharge_orders%ROWTYPE;
    refund payment_refunds%ROWTYPE;
    charged_balance INT;
    bonus_balance INT;
    charged_debit INT;
    bonus_debit INT;
    prior_charged INT;
    prior_bonus INT;
BEGIN
    SELECT * INTO STRICT purchase FROM recharge_orders WHERE id = target_order FOR UPDATE;
    SELECT * INTO STRICT refund FROM payment_refunds WHERE order_id = target_order FOR UPDATE;
    IF refund.status = 'Completed' THEN
        IF refund.provider_refund_id <> provider_refund THEN RAISE EXCEPTION 'Refund ID mismatch'; END IF;
        RETURN;
    END IF;
    IF purchase.status <> 'RefundPending' OR purchase.payment_method <> 'paypal'
        OR refund.status <> 'Pending'
        OR (refund.provider_refund_id IS NOT NULL AND refund.provider_refund_id <> provider_refund)
        OR provider_refund = '' THEN
        RAISE EXCEPTION 'Invalid refund transition';
    END IF;
    SELECT charged_coins,bonus_coins INTO prior_charged,prior_bonus
        FROM payment_entitlement_adjustments WHERE order_id=target_order AND blocked;
    SELECT charged_coins, bonus_coins INTO charged_balance, bonus_balance
        FROM wallets WHERE user_id = purchase.user_id FOR UPDATE;
    charged_debit := LEAST(GREATEST(0, COALESCE(charged_balance,0)), GREATEST(0,purchase.coins-COALESCE(prior_charged,0)));
    bonus_debit := LEAST(GREATEST(0, COALESCE(bonus_balance,0)), GREATEST(0,purchase.bonus_coins_credited-COALESCE(prior_bonus,0)));
    IF prior_charged IS NOT NULL THEN
        -- The approved total entitlement loss already consumed this grant.
        charged_debit := 0;
        bonus_debit := 0;
    ELSIF purchase.fulfilled_at IS NULL AND EXISTS(SELECT 1 FROM payment_deferred_fulfillment WHERE order_id=target_order) THEN
        charged_debit := 0;
        bonus_debit := 0;
        prior_charged := purchase.coins;
        prior_bonus := purchase.bonus_coins_credited;
    END IF;
    IF charged_debit + bonus_debit > 0 THEN
        UPDATE wallets SET charged_coins = charged_coins - charged_debit,
            bonus_coins = bonus_coins - bonus_debit, updated_at = now() WHERE user_id = purchase.user_id;
        INSERT INTO transactions (id,user_id,type,biz_type,amount,charged_amount,bonus_amount,description)
            VALUES (gen_random_uuid()::text,purchase.user_id,'debit','refund',charged_debit+bonus_debit,
                    charged_debit,bonus_debit,'Refund for order #' || target_order);
    END IF;
    UPDATE payment_refunds SET status='Completed', provider_refund_id=provider_refund,
        charged_coin_shortfall=GREATEST(0,purchase.coins-charged_debit-COALESCE(prior_charged,0)),
        bonus_coin_shortfall=GREATEST(0,purchase.bonus_coins_credited-bonus_debit-COALESCE(prior_bonus,0)),
        updated_at=now() WHERE order_id=target_order;
    UPDATE recharge_orders SET status='Refunded', updated_at=now() WHERE id=target_order;
    IF purchase.subscription_id IS NOT NULL AND purchase.paid_period_end IS NOT NULL THEN
        PERFORM 1 FROM user_subscriptions WHERE subscription_id=purchase.subscription_id FOR UPDATE;
        UPDATE user_subscriptions SET current_period_end = (
            SELECT max(paid_period_end) FROM recharge_orders
            WHERE subscription_id = purchase.subscription_id AND status IN ('Success','Paid','RefundPending')
              AND NOT paypal_refund_reported
        ), updated_at = now()
        WHERE subscription_id = purchase.subscription_id AND current_period_end <= purchase.paid_period_end;
    END IF;
END;
$$;

INSERT INTO reader_schema_migrations(version) VALUES (2026111922);
