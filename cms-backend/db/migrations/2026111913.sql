-- Called after recording a verified capture while holding the subscription lock.
CREATE FUNCTION reader_subscription_payment_kind(target_order bigint)
RETURNS text LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN s.capi_subscribe_state='claimed' AND s.capi_subscribe_order_id=o.id THEN 'subscription_initial'
        WHEN s.capi_subscribe_state='claimed' AND EXISTS (
            SELECT 1
            FROM recharge_orders first_order
            JOIN third_party_payment_orders first_capture ON first_capture.order_id=first_order.id
            WHERE first_order.id=s.capi_subscribe_order_id
              AND first_order.subscription_id=o.subscription_id AND first_order.user_id=o.user_id
              AND first_order.order_type='subscription' AND first_order.payment_method=o.payment_method
              AND first_order.fulfilled_at IS NOT NULL
              AND first_capture.payment_provider=o.payment_method AND first_capture.status='VERIFIED'
              AND first_capture.external_order_id=o.subscription_id AND first_capture.capture_id=first_order.external_ref_id
        ) THEN 'subscription_renewal'
        WHEN s.capi_subscribe_state IN ('historical','unknown') AND EXISTS (
            SELECT 1
            FROM third_party_payment_orders prior_capture
            JOIN recharge_orders prior ON prior.id=prior_capture.order_id
            WHERE prior_capture.payment_provider=o.payment_method AND prior_capture.external_order_id=o.subscription_id
              AND prior_capture.status='VERIFIED' AND prior_capture.capture_id=prior.external_ref_id
              AND prior.subscription_id=o.subscription_id AND prior.user_id=o.user_id
              AND prior.order_type='subscription' AND prior.fulfilled_at IS NOT NULL AND prior.paid_at<o.paid_at
        ) THEN 'subscription_renewal'
    END
    FROM recharge_orders o
    JOIN user_subscriptions s ON s.subscription_id=o.subscription_id AND s.user_id=o.user_id
    JOIN third_party_payment_orders capture ON capture.order_id=o.id AND capture.payment_provider=o.payment_method
        AND capture.external_order_id=o.subscription_id AND capture.capture_id=o.external_ref_id AND capture.status='VERIFIED'
    WHERE o.id=target_order AND o.order_type='subscription'
$$;

-- Expose only the fact for a verified source order and its own attribution.
CREATE FUNCTION reader_verified_first_payment(event_environment text, target_order bigint, target_user bigint, target_promotion integer)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
    SELECT first_order.id=o.id
    FROM public.recharge_orders o
    JOIN public.reader_payment_first_on_promotion f ON f.user_id=o.user_id AND f.promotion_link_id=target_promotion
    JOIN public.recharge_orders first_order ON first_order.id=f.order_id AND first_order.user_id=o.user_id
    WHERE o.id=target_order AND o.user_id=target_user
      AND o.capi_expectation->>'environment'=event_environment AND o.capi_expectation->>'payment_key'=o.id::text
      AND o.fulfilled_at IS NOT NULL AND o.amount_cents>0 AND o.status IN ('Success','Paid','RefundPending','Refunded')
      AND public.reader_payment_promotion_key(o.promotion_link_id,o.fb_lead_metadata)=target_promotion
      AND first_order.fulfilled_at IS NOT NULL AND first_order.amount_cents>0
      AND first_order.status IN ('Success','Paid','RefundPending','Refunded')
      AND public.reader_payment_promotion_key(first_order.promotion_link_id,first_order.fb_lead_metadata)=target_promotion
      AND COALESCE(first_order.capi_expectation->>'environment',event_environment)=event_environment
      AND public.reader_payment_coverage_verified(event_environment,target_promotion)
$$;
REVOKE EXECUTE ON FUNCTION reader_verified_first_payment(text,bigint,bigint,integer) FROM PUBLIC;

CREATE OR REPLACE FUNCTION record_reader_payment_conversion(
    target_order bigint, original_time timestamptz, time_source text, event_environment text,
    include_subscribe boolean, automatic_payment boolean, subscription_kind text
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
            WHEN purchase.order_type='subscription' THEN subscription_kind
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

-- Keep the six-argument Reader interface operational during a rolling upgrade.
CREATE OR REPLACE FUNCTION record_reader_payment_conversion(
    target_order bigint, original_time timestamptz, time_source text, event_environment text,
    include_subscribe boolean, automatic_payment boolean
) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    subscription_kind text;
BEGIN
    IF include_subscribe THEN
        subscription_kind := 'subscription_initial';
    ELSIF EXISTS (SELECT 1 FROM recharge_orders WHERE id=target_order AND order_type='subscription') THEN
        subscription_kind := reader_subscription_payment_kind(target_order);
    END IF;
    PERFORM record_reader_payment_conversion(
        target_order, original_time, time_source, event_environment, include_subscribe,
        automatic_payment OR COALESCE(subscription_kind='subscription_renewal',false), subscription_kind);
END $$;

INSERT INTO reader_schema_migrations (version) VALUES (2026111913);
