ALTER TABLE recharge_orders
    ADD COLUMN checkout_environment TEXT,
    ADD COLUMN checkout_return_path TEXT NOT NULL DEFAULT '';

ALTER TABLE payment_channel_accounts
    ADD COLUMN new_orders_enabled BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN new_subscriptions_enabled BOOLEAN NOT NULL DEFAULT true;

CREATE INDEX idx_whole_book_checkout_pending ON recharge_orders (user_id, novel_id, id)
    WHERE order_type='whole_book' AND status='Pending';

CREATE TABLE payment_checkout_requests (
    environment TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_key UUID NOT NULL,
    slot_id INT NOT NULL,
    novel_id BIGINT NOT NULL,
    return_path TEXT NOT NULL,
    order_id BIGINT NOT NULL REFERENCES recharge_orders(id) ON DELETE CASCADE,
    PRIMARY KEY (environment, user_id, request_key)
);

CREATE TABLE payment_checkout_attempts (
    id UUID PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES recharge_orders(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    account_id BIGINT NOT NULL,
    sales_mode TEXT NOT NULL CHECK (sales_mode IN ('direct','merchant_of_record')),
    status TEXT NOT NULL CHECK (status IN ('creating','buyer_action','capturing','settled','closed')),
    provider_order_id TEXT,
    approval_url TEXT,
    return_url TEXT NOT NULL,
    cancel_url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_sent_at TIMESTAMPTZ,
    retry_until TIMESTAMPTZ,
    create_lookup_only BOOLEAN NOT NULL DEFAULT false,
    next_create_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    capture_first_sent_at TIMESTAMPTZ,
    capture_retry_until TIMESTAMPTZ,
    capture_lookup_only BOOLEAN NOT NULL DEFAULT false,
    next_capture_at TIMESTAMPTZ,
    FOREIGN KEY (provider,account_id) REFERENCES payment_channel_accounts(provider,id),
    UNIQUE (account_id,provider_order_id)
);

CREATE UNIQUE INDEX idx_payment_checkout_active ON payment_checkout_attempts (order_id)
    WHERE status IN ('creating','buyer_action','capturing');
CREATE UNIQUE INDEX idx_paypal_checkout_order ON payment_checkout_attempts (provider_order_id)
    WHERE provider='paypal' AND provider_order_id IS NOT NULL;
CREATE INDEX idx_paypal_checkout_create_due ON payment_checkout_attempts (next_create_at,id)
    WHERE provider='paypal' AND status='creating';

ALTER TABLE third_party_payment_orders
    ADD CONSTRAINT payment_receipt_order_key UNIQUE (order_id, id),
    ADD COLUMN account_id BIGINT,
    ADD COLUMN object_type TEXT,
    ADD COLUMN reconciliation_note TEXT,
    ADD CONSTRAINT payment_receipt_account FOREIGN KEY (payment_provider,account_id)
        REFERENCES payment_channel_accounts(provider,id),
    ADD CONSTRAINT payment_receipt_scoped_identity CHECK (
        account_id IS NULL OR (object_type IS NOT NULL AND capture_id IS NOT NULL AND capture_id <> '')
    );
CREATE UNIQUE INDEX idx_payment_receipt_identity
    ON third_party_payment_orders (account_id,object_type,capture_id) WHERE account_id IS NOT NULL;

CREATE TABLE payment_fulfillment_funding (
    order_id BIGINT NOT NULL,
    receipt_id BIGINT NOT NULL,
    principal_minor BIGINT NOT NULL CHECK (principal_minor > 0),
    currency VARCHAR(3) NOT NULL,
    PRIMARY KEY (order_id,receipt_id),
    FOREIGN KEY (order_id,receipt_id) REFERENCES third_party_payment_orders(order_id,id) ON DELETE CASCADE
);

INSERT INTO payment_fulfillment_funding (order_id,receipt_id,principal_minor,currency)
SELECT o.id,p.id,o.amount_cents,o.currency
FROM recharge_orders o
JOIN LATERAL (
    SELECT min(id) AS id FROM third_party_payment_orders
    WHERE order_id=o.id AND payment_provider='paypal' AND status='VERIFIED'
    HAVING count(*)=1
) evidence ON true
JOIN third_party_payment_orders p ON p.id=evidence.id
WHERE o.payment_method='paypal' AND o.fulfilled_at IS NOT NULL
  AND o.status IN ('Success','Paid','RefundPending','Refunded')
  AND o.amount_cents>0 AND p.gross_amount*100=o.amount_cents AND p.currency=o.currency
  AND p.capture_id<>'' AND p.capture_id NOT LIKE 'mock%'
  AND (o.order_type<>'whole_book' OR (o.novel_id>0 AND (o.checkout_id IS NOT NULL OR EXISTS (
      SELECT 1 FROM legacy_whole_book_proofs proof WHERE proof.order_id=o.id AND proof.novel_id=o.novel_id))))
  AND (o.order_type<>'subscription' OR (o.paid_period_end>o.paid_period_start AND p.capture_id IS DISTINCT FROM o.subscription_id));

ALTER TABLE paypal_subscription_intents
    ADD COLUMN checkout_account_id BIGINT REFERENCES payment_channel_accounts(id),
    ADD COLUMN checkout_return_path TEXT NOT NULL DEFAULT '';
ALTER TABLE user_subscriptions
    ADD COLUMN checkout_account_id BIGINT REFERENCES payment_channel_accounts(id),
    ADD COLUMN checkout_intent_id UUID UNIQUE,
    ADD COLUMN checkout_return_path TEXT NOT NULL DEFAULT '',
    ADD COLUMN paypal_activation_requested_at TIMESTAMPTZ;

CREATE TABLE payment_refund_commands (
    id UUID PRIMARY KEY,
    environment TEXT NOT NULL,
    actor_id TEXT,
    request_key UUID NOT NULL,
    order_id BIGINT NOT NULL REFERENCES recharge_orders(id) ON DELETE CASCADE,
    receipt_id BIGINT,
    amount_minor BIGINT CHECK (amount_minor > 0),
    currency VARCHAR(3) NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','confirming','succeeded','failed','review')),
    provider_refund_id TEXT UNIQUE,
    legacy_source BOOLEAN NOT NULL DEFAULT false,
    first_sent_at TIMESTAMPTZ,
    next_check_at TIMESTAMPTZ,
    claimed_until TIMESTAMPTZ,
    result_note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (environment,actor_id,request_key),
    FOREIGN KEY (order_id,receipt_id) REFERENCES third_party_payment_orders(order_id,id) ON DELETE CASCADE,
    CHECK (legacy_source OR (actor_id IS NOT NULL AND receipt_id IS NOT NULL AND amount_minor IS NOT NULL))
);
CREATE INDEX idx_payment_refund_commands_due ON payment_refund_commands(next_check_at,id) WHERE next_check_at IS NOT NULL;
CREATE INDEX idx_payment_refund_commands_receipt ON payment_refund_commands(receipt_id,created_at DESC);
CREATE INDEX idx_payment_refund_commands_order ON payment_refund_commands(order_id,created_at DESC);
CREATE INDEX idx_payment_refund_commands_legacy_order ON payment_refund_commands(order_id)
    WHERE receipt_id IS NULL AND status='review';

-- A legacy refund was already eligible for transmission. Its updated_at
-- can be a worker lease. Even with a known capture the new worker only looks up.
INSERT INTO payment_refund_commands
    (id,environment,actor_id,request_key,order_id,receipt_id,amount_minor,currency,status,provider_refund_id,
     legacy_source,next_check_at,result_note)
SELECT r.request_id,COALESCE(l.environment,'unknown'),r.admin_id,r.request_id,o.id,
       CASE WHEN evidence.matches=1 THEN evidence.id END,
       CASE WHEN r.requested_amount_cents>0 THEN r.requested_amount_cents END,COALESCE(o.currency,'USD'),
       CASE WHEN evidence.matches=1 AND l.environment IS NOT NULL AND r.requested_amount_cents>0 THEN 'confirming' ELSE 'review' END,
       r.provider_refund_id,true,
       CASE WHEN evidence.matches=1 AND l.environment IS NOT NULL AND r.requested_amount_cents>0 THEN now() END,
       'Migrated PayPal refund with unknown first transmission time'
FROM payment_refunds r JOIN recharge_orders o ON o.id=r.order_id
LEFT JOIN paypal_identity_lock l ON l.id
CROSS JOIN LATERAL (
    SELECT count(*) AS matches, min(p.id) AS id
    FROM third_party_payment_orders p
    WHERE p.order_id=o.id AND p.payment_provider='paypal' AND p.status IN ('VERIFIED','COMPLETED')
      AND p.capture_id<>'' AND p.capture_id NOT LIKE 'mock%' AND p.currency=o.currency AND p.gross_amount*100=o.amount_cents
      AND (o.order_type<>'subscription' OR p.capture_id IS DISTINCT FROM o.subscription_id)
) evidence
WHERE r.status='Pending' AND o.payment_method='paypal';

CREATE OR REPLACE FUNCTION settle_paypal_refund_item(target_order BIGINT, refund_ref TEXT, capture_ref TEXT, cents BIGINT, currency_code TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql AS $$
DECLARE
    purchase recharge_orders%ROWTYPE;
    refunded NUMERIC;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('paypal-capture:' || capture_ref,0));
    SELECT * INTO STRICT purchase FROM recharge_orders WHERE id=target_order FOR UPDATE;
    IF purchase.payment_method <> 'paypal' OR purchase.currency <> currency_code
        OR purchase.status NOT IN ('Success','Paid','RefundPending','Refunded') THEN
        RAISE EXCEPTION 'Invalid PayPal refund target';
    END IF;
    PERFORM save_paypal_refund_item(refund_ref,capture_ref,cents,currency_code);
    IF EXISTS (SELECT 1 FROM payment_fulfillment_funding f WHERE f.order_id=target_order)
        AND NOT EXISTS (
            SELECT 1 FROM payment_fulfillment_funding f JOIN third_party_payment_orders p ON p.id=f.receipt_id
            WHERE f.order_id=target_order AND p.payment_provider='paypal' AND p.capture_id=capture_ref
        ) THEN
        RETURN false;
    END IF;
    IF purchase.status='Refunded' THEN RETURN true; END IF;
    SELECT SUM(amount_cents) INTO refunded FROM paypal_refund_items
        WHERE capture_id=capture_ref AND currency=currency_code;
    IF refunded < purchase.amount_cents THEN RETURN false; END IF;
    IF refunded <> purchase.amount_cents THEN RAISE EXCEPTION 'PayPal refund total differs from order'; END IF;

    INSERT INTO payment_refunds(order_id,request_id,provider_refund_id,status,prior_order_status)
        VALUES(target_order,gen_random_uuid(),refund_ref,'Pending',purchase.status)
        ON CONFLICT (order_id) DO UPDATE SET provider_refund_id=EXCLUDED.provider_refund_id,status='Pending'
        WHERE payment_refunds.status IN ('Pending','Failed');
    UPDATE recharge_orders SET status='RefundPending',updated_at=now() WHERE id=target_order;
    PERFORM complete_paypal_refund(target_order,refund_ref);
    RETURN true;
END;
$$;

INSERT INTO reader_schema_migrations (version) VALUES (2026111917);
