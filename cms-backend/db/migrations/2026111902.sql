DROP TRIGGER payment_events ON recharge_orders;
DROP FUNCTION capture_payment_events();
DROP FUNCTION enqueue_payment_event(recharge_orders, text);

-- Preserve the legacy queue's existing refund cleanup while it awaits takeover.
CREATE FUNCTION discard_unfulfilled_legacy_conversion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM payment_event_outbox WHERE order_id=NEW.id AND delivered_at IS NULL;
    RETURN NULL;
END $$;
CREATE TRIGGER discard_unfulfilled_legacy_conversion AFTER UPDATE OF status, fulfilled_at ON recharge_orders
FOR EACH ROW WHEN (NEW.status='Refunded' AND NEW.fulfilled_at IS NULL
    AND (NEW.status,NEW.fulfilled_at) IS DISTINCT FROM (OLD.status,OLD.fulfilled_at))
EXECUTE FUNCTION discard_unfulfilled_legacy_conversion();

ALTER TABLE reader_conversion_outbox
    ADD COLUMN environment text NOT NULL DEFAULT 'unknown',
    ADD COLUMN processing_error text;
ALTER TABLE conversion.events ADD COLUMN order_type text, ADD COLUMN time_source text;
DROP INDEX reader_conversion_outbox_pending;
CREATE INDEX reader_conversion_outbox_pending
    ON reader_conversion_outbox (environment, created_at, business_key) WHERE consumed_at IS NULL;

-- The caller has already settled the verified payment and locked the order.
-- For subscriptions it has also acquired the subscription row in the existing
-- capture -> order -> subscription lock order.
CREATE FUNCTION record_reader_payment_conversion(
    target_order bigint, original_time timestamptz, time_source text, event_environment text,
    include_subscribe boolean, automatic_payment boolean
) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    purchase recharge_orders%ROWTYPE;
    pixel text;
    event_reason text;
    purchase_key text;
    subscribe_key text;
    event_specs jsonb := '[]';
    attribution jsonb;
    context_snapshot jsonb;
    charge_email text;
BEGIN
    SELECT * INTO STRICT purchase FROM recharge_orders WHERE id = target_order FOR UPDATE;
    IF purchase.capi_expectation IS NOT NULL THEN RETURN; END IF;
    IF purchase.status = 'Refunded' AND purchase.fulfilled_at IS NULL THEN
        event_reason := 'refunded_before_fulfillment';
    ELSIF purchase.status NOT IN ('Success','Paid') OR purchase.paypal_refund_reported OR purchase.fulfilled_at IS NULL THEN
        RETURN;
    ELSE
        attribution := COALESCE(purchase.fb_lead_metadata, '{}'::jsonb);
        pixel := NULLIF(attribution->>'pixel_id', '');
        event_reason := CASE
            WHEN pixel IS NOT NULL THEN 'matched'
            WHEN purchase.promotion_link_id IS NOT NULL OR NULLIF(attribution->>'promotion_link_id','') IS NOT NULL
                 OR COALESCE(purchase.utm_source,'') <> '' OR COALESCE(purchase.utm_campaign,'') <> ''
                 OR COALESCE(attribution->>'fbc','') <> '' THEN 'missing_pixel'
            ELSE 'organic'
        END;
        IF event_reason <> 'organic' THEN
            purchase_key := 'payment:' || purchase.id || ':Purchase';
            event_specs := jsonb_build_array(jsonb_build_object('name','Purchase','business_key',purchase_key));
            IF include_subscribe THEN
                subscribe_key := 'subscription:' || purchase.subscription_id || ':Subscribe';
                event_specs := event_specs || jsonb_build_array(jsonb_build_object('name','Subscribe','business_key',subscribe_key));
            END IF;
            SELECT COALESCE(NULLIF(email,''), NULLIF(attribution->>'payer_email',''),
                (SELECT NULLIF(p.payer_email,'') FROM third_party_payment_orders p
                 WHERE p.order_id=purchase.id AND p.status='VERIFIED' ORDER BY p.id DESC LIMIT 1), '')
              INTO STRICT charge_email FROM users WHERE id = purchase.user_id;
            context_snapshot := jsonb_build_object(
                'user_id', purchase.user_id::text, 'email', charge_email, 'amount_cents', purchase.amount_cents,
                'currency', purchase.currency, 'order_type', purchase.order_type, 'attribution_state', event_reason,
                'reason', CASE WHEN original_time IS NULL THEN 'event_time_missing' ELSE event_reason END,
                'time_source', time_source, 'attribution', attribution,
                'promotion_id', COALESCE(attribution->>'promotion_link_id', purchase.promotion_link_id::text),
                'action_source', CASE WHEN automatic_payment THEN 'system_generated' ELSE 'website' END
            );
        END IF;
    END IF;
    UPDATE recharge_orders SET capi_expectation = jsonb_build_object(
        'events', event_specs, 'pixel_id', pixel,
        'reason', CASE WHEN original_time IS NULL AND purchase_key IS NOT NULL THEN 'event_time_missing' ELSE event_reason END,
        'attribution_state', event_reason, 'environment', event_environment,
        'occurred_at', original_time, 'time_source', time_source,
        'fulfilled_at', purchase.fulfilled_at
    ) WHERE id = target_order;
    IF purchase_key IS NOT NULL THEN
        INSERT INTO reader_conversion_outbox (business_key,event_name,order_id,environment,occurred_at,snapshot)
        SELECT entry->>'business_key', entry->>'name', target_order, event_environment, original_time, context_snapshot
        FROM jsonb_array_elements(event_specs) entry
        ON CONFLICT (business_key) DO NOTHING;
    END IF;
END $$;

INSERT INTO reader_schema_migrations (version) VALUES (2026111902);
