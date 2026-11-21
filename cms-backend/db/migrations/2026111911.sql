-- Run during the Reader settlement cutover with payment writes stopped.
LOCK TABLE recharge_orders IN SHARE ROW EXCLUSIVE MODE;

CREATE FUNCTION reader_payment_promotion_key(link_id integer, attribution jsonb)
RETURNS integer LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE
        WHEN attribution->>'promotion_link_id' ~ '^[1-9][0-9]{0,9}$' THEN
            CASE WHEN (attribution->>'promotion_link_id')::bigint <= 2147483647
                 THEN (attribution->>'promotion_link_id')::integer ELSE link_id END
        ELSE link_id END
$$;

CREATE TABLE reader_payment_first_on_promotion (
    user_id bigint NOT NULL,
    promotion_link_id integer NOT NULL,
    order_id bigint NOT NULL,
    PRIMARY KEY (user_id, promotion_link_id)
);

-- These are verified candidates from the available orders. Coverage of a
-- pre-cutover link is reviewed independently before publishing first-only.
INSERT INTO reader_payment_first_on_promotion (user_id,promotion_link_id,order_id)
SELECT DISTINCT ON (user_id, promotion) user_id,promotion,id
FROM (
    SELECT user_id,reader_payment_promotion_key(promotion_link_id,fb_lead_metadata) AS promotion,id,fulfilled_at
    FROM recharge_orders
    WHERE fulfilled_at IS NOT NULL AND amount_cents>0 AND status IN ('Success','Paid','RefundPending','Refunded')
      AND (promotion_link_id IS NOT NULL OR fb_lead_metadata ? 'promotion_link_id')
) fulfilled
WHERE promotion IS NOT NULL
ORDER BY user_id,promotion,fulfilled_at,id;

CREATE FUNCTION reader_payment_coverage_verified(event_environment text, link integer)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
    SELECT EXISTS (
        SELECT 1 FROM public.promotion_links p
        WHERE p.id=link AND (
            p.created_at >= (SELECT applied_at FROM public.reader_schema_migrations WHERE version=2026111911)
            OR EXISTS (
                SELECT 1 FROM public.reader_payment_promotion_coverage c
                WHERE c.environment=event_environment AND c.promotion_link_id=link)))
$$;
REVOKE EXECUTE ON FUNCTION reader_payment_coverage_verified(text,integer) FROM PUBLIC;

CREATE FUNCTION lock_reader_first_payment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    promotion integer;
BEGIN
    IF NEW.fulfilled_at IS NULL OR NEW.status NOT IN ('Success','Paid') OR NEW.amount_cents<=0
       OR NEW.paypal_refund_reported THEN
        RETURN NEW;
    END IF;
    IF TG_OP='UPDATE' AND OLD.fulfilled_at IS NOT NULL THEN
        RETURN NEW;
    END IF;
    promotion := reader_payment_promotion_key(NEW.promotion_link_id,NEW.fb_lead_metadata);
    IF promotion IS NOT NULL THEN
        PERFORM pg_advisory_xact_lock(hashtextextended('reader-first-payment:'||NEW.user_id||':'||promotion,0));
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER lock_reader_first_payment
    BEFORE INSERT OR UPDATE OF fulfilled_at ON recharge_orders
    FOR EACH ROW EXECUTE FUNCTION lock_reader_first_payment();

CREATE INDEX payment_event_outbox_confirmed_order ON payment_event_outbox (order_id)
    WHERE state='delivered' OR delivered_at IS NOT NULL;

CREATE INDEX conversion_events_reader_payment_policy_pending
    ON conversion.events (integration_id,environment,payment_app_scope,source_order_id,id)
    WHERE payment_policy_state='pending';

CREATE OR REPLACE FUNCTION record_reader_payment_conversion(
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
    enqueued_at timestamptz;
    promotion integer;
    first_order bigint;
    first_payment boolean;
    payment_kind text;
BEGIN
    SELECT * INTO STRICT purchase FROM recharge_orders WHERE id = target_order FOR UPDATE;
    IF purchase.capi_expectation IS NOT NULL THEN RETURN; END IF;
    IF purchase.status = 'Refunded' AND purchase.fulfilled_at IS NULL THEN
        event_reason := 'refunded_before_fulfillment';
    ELSIF purchase.status NOT IN ('Success','Paid') OR purchase.paypal_refund_reported OR purchase.fulfilled_at IS NULL THEN
        RETURN;
    ELSE
        attribution := COALESCE(purchase.fb_lead_metadata, '{}'::jsonb);
        promotion := reader_payment_promotion_key(purchase.promotion_link_id,attribution);
        IF promotion IS NOT NULL AND purchase.amount_cents>0 THEN
            -- The fulfillment trigger already holds this lock in normal
            -- settlement. Also acquire it for explicit calls on saved orders.
            PERFORM pg_advisory_xact_lock(hashtextextended('reader-first-payment:'||purchase.user_id||':'||promotion,0));
            INSERT INTO reader_payment_first_on_promotion (user_id,promotion_link_id,order_id)
            VALUES (purchase.user_id,promotion,purchase.id) ON CONFLICT (user_id,promotion_link_id) DO NOTHING;
            IF reader_payment_coverage_verified(event_environment,promotion) THEN
                SELECT order_id INTO STRICT first_order FROM reader_payment_first_on_promotion
                WHERE user_id=purchase.user_id AND promotion_link_id=promotion;
                first_payment := first_order = purchase.id;
            END IF;
        END IF;
        payment_kind := CASE
            WHEN purchase.order_type='subscription' AND include_subscribe THEN 'subscription_initial'
            WHEN purchase.order_type='subscription' AND automatic_payment THEN 'subscription_renewal'
            WHEN purchase.order_type IN ('single','whole_book') THEN purchase.order_type
        END;
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
                'promotion_id', promotion::text,
                'payment_policy_version', 1, 'payment_key', purchase.id::text,
                'payment_kind', payment_kind, 'first_paid_on_promotion', first_payment,
                'action_source', CASE WHEN automatic_payment THEN 'system_generated' ELSE 'website' END
            );
        END IF;
    END IF;
    UPDATE recharge_orders SET capi_expectation = jsonb_build_object(
        'events', event_specs, 'pixel_id', pixel,
        'reason', CASE WHEN original_time IS NULL AND purchase_key IS NOT NULL THEN 'event_time_missing' ELSE event_reason END,
        'attribution_state', event_reason, 'environment', event_environment,
        'occurred_at', original_time, 'time_source', time_source,
        'fulfilled_at', purchase.fulfilled_at,
        'payment_policy_version', 1, 'payment_key', purchase.id::text,
        'payment_kind', payment_kind, 'first_paid_on_promotion', first_payment
    ) WHERE id = target_order;
    IF purchase_key IS NOT NULL THEN
        enqueued_at := clock_timestamp();
        INSERT INTO reader_conversion_repair_facts (business_key,event_name,order_id,environment,occurred_at,created_at,snapshot)
        SELECT entry->>'business_key', entry->>'name', target_order, event_environment, original_time, enqueued_at, context_snapshot
        FROM jsonb_array_elements(event_specs) entry
        ON CONFLICT (business_key) DO NOTHING;

        INSERT INTO reader_conversion_outbox (business_key,event_name,order_id,environment,occurred_at,created_at,snapshot)
        SELECT entry->>'business_key', entry->>'name', target_order, event_environment, original_time, enqueued_at, context_snapshot
        FROM jsonb_array_elements(event_specs) entry
        ON CONFLICT (business_key) DO NOTHING;
    END IF;
END $$;

INSERT INTO reader_schema_migrations (version,applied_at) VALUES (2026111911,clock_timestamp());
