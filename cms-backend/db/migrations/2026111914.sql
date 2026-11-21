-- Share verified-capture classification between settlement and fact recovery.
CREATE OR REPLACE FUNCTION reader_subscription_payment_kind(target_order bigint)
RETURNS text LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
    SELECT CASE
        WHEN s.capi_subscribe_state='claimed' AND s.capi_subscribe_order_id=o.id THEN 'subscription_initial'
        WHEN s.capi_subscribe_state='claimed' AND EXISTS (
            SELECT 1
            FROM public.recharge_orders first_order
            JOIN public.third_party_payment_orders first_capture ON first_capture.order_id=first_order.id
            WHERE first_order.id=s.capi_subscribe_order_id
              AND first_order.subscription_id=o.subscription_id AND first_order.user_id=o.user_id
              AND first_order.order_type='subscription' AND first_order.payment_method=o.payment_method
              AND first_order.fulfilled_at IS NOT NULL
              AND (o.capi_expectation->>'environment' IS NULL OR first_order.capi_expectation->>'environment' IS NULL
                   OR first_order.capi_expectation->>'environment'=o.capi_expectation->>'environment')
              AND first_capture.payment_provider=o.payment_method AND first_capture.status='VERIFIED'
              AND first_capture.external_order_id=o.subscription_id AND first_capture.capture_id=first_order.external_ref_id
        ) THEN 'subscription_renewal'
        WHEN s.capi_subscribe_state IN ('historical','unknown') AND EXISTS (
            SELECT 1
            FROM public.third_party_payment_orders prior_capture
            JOIN public.recharge_orders prior ON prior.id=prior_capture.order_id
            WHERE prior_capture.payment_provider=o.payment_method AND prior_capture.external_order_id=o.subscription_id
              AND prior_capture.status='VERIFIED' AND prior_capture.capture_id=prior.external_ref_id
              AND prior.subscription_id=o.subscription_id AND prior.user_id=o.user_id
              AND prior.order_type='subscription' AND prior.payment_method=o.payment_method
              AND prior.fulfilled_at IS NOT NULL AND prior.paid_at<o.paid_at
              AND (o.capi_expectation->>'environment' IS NULL OR prior.capi_expectation->>'environment' IS NULL
                   OR prior.capi_expectation->>'environment'=o.capi_expectation->>'environment')
        ) THEN 'subscription_renewal'
    END
    FROM public.recharge_orders o
    JOIN public.user_subscriptions s ON s.subscription_id=o.subscription_id AND s.user_id=o.user_id
    JOIN public.third_party_payment_orders capture ON capture.order_id=o.id AND capture.payment_provider=o.payment_method
        AND capture.external_order_id=o.subscription_id AND capture.capture_id=o.external_ref_id AND capture.status='VERIFIED'
    WHERE o.id=target_order AND o.order_type='subscription'
$$;

-- Runtime roles can resolve a missing kind for the exact fulfilled source order.
CREATE FUNCTION reader_verified_subscription_payment_kind(event_environment text, target_order bigint, target_user bigint)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
    SELECT public.reader_subscription_payment_kind(o.id)
    FROM public.recharge_orders o
    WHERE o.id=target_order AND o.user_id=target_user AND o.order_type='subscription'
      AND o.capi_expectation->>'environment'=event_environment AND o.capi_expectation->>'payment_key'=o.id::text
      AND o.fulfilled_at IS NOT NULL AND o.amount_cents>0 AND o.status IN ('Success','Paid','RefundPending','Refunded')
$$;
REVOKE EXECUTE ON FUNCTION reader_verified_subscription_payment_kind(text,bigint,bigint) FROM PUBLIC;

INSERT INTO reader_schema_migrations (version) VALUES (2026111914);
