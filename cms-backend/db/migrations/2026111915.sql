-- Share verified-capture classification between settlement and fact recovery.
CREATE FUNCTION reader_subscription_payment_kind(target_order bigint, event_environment text)
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
              AND COALESCE(first_order.capi_expectation->>'environment',event_environment)=event_environment
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
              AND COALESCE(prior.capi_expectation->>'environment',event_environment)=event_environment
        ) THEN 'subscription_renewal'
    END
    FROM public.recharge_orders o
    JOIN public.user_subscriptions s ON s.subscription_id=o.subscription_id AND s.user_id=o.user_id
    JOIN public.third_party_payment_orders capture ON capture.order_id=o.id AND capture.payment_provider=o.payment_method
        AND capture.external_order_id=o.subscription_id AND capture.capture_id=o.external_ref_id AND capture.status='VERIFIED'
    WHERE o.id=target_order AND o.order_type='subscription'
      AND NULLIF(event_environment,'') IS NOT NULL
      AND COALESCE(o.capi_expectation->>'environment',event_environment)=event_environment
$$;

-- The older lookup can only infer the environment for an already stamped order.
CREATE OR REPLACE FUNCTION reader_subscription_payment_kind(target_order bigint)
RETURNS text LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
    SELECT public.reader_subscription_payment_kind(o.id,o.capi_expectation->>'environment')
    FROM public.recharge_orders o WHERE o.id=target_order
$$;

-- Runtime roles can resolve a missing kind for the exact fulfilled source order.
CREATE OR REPLACE FUNCTION reader_verified_subscription_payment_kind(event_environment text, target_order bigint, target_user bigint)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
    SELECT public.reader_subscription_payment_kind(o.id,event_environment)
    FROM public.recharge_orders o
    WHERE o.id=target_order AND o.user_id=target_user AND o.order_type='subscription'
      AND o.capi_expectation->>'environment'=event_environment AND o.capi_expectation->>'payment_key'=o.id::text
      AND o.fulfilled_at IS NOT NULL AND o.amount_cents>0 AND o.status IN ('Success','Paid','RefundPending','Refunded')
$$;
REVOKE EXECUTE ON FUNCTION reader_verified_subscription_payment_kind(text,bigint,bigint) FROM PUBLIC;

-- Classify subscription payments with the caller's environment, including legacy writes.
CREATE OR REPLACE FUNCTION record_reader_payment_conversion(
    target_order bigint, original_time timestamptz, time_source text, event_environment text,
    include_subscribe boolean, automatic_payment boolean
) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    subscription_kind text;
    is_automatic boolean := automatic_payment;
BEGIN
    IF EXISTS (SELECT 1 FROM recharge_orders WHERE id=target_order AND order_type='subscription') THEN
        subscription_kind := CASE WHEN include_subscribe THEN 'subscription_initial'
                                  ELSE reader_subscription_payment_kind(target_order,event_environment) END;
        is_automatic := COALESCE(subscription_kind='subscription_renewal',false);
    END IF;
    PERFORM record_reader_payment_conversion(
        target_order, original_time, time_source, event_environment, include_subscribe, is_automatic, subscription_kind);
END $$;

-- Recover unattempted pre-upgrade failures that still have time to gather evidence.
WITH candidates AS (
    SELECT d.id,d.event_id
    FROM conversion.deliveries d
    JOIN conversion.events e ON e.id=d.event_id AND e.environment=d.environment
    JOIN conversion.integrations i ON i.id=e.integration_id AND i.environment=e.environment AND i.source='starnovel'
    JOIN reader_conversion_repair_facts f ON f.business_key=e.source_event_id AND f.environment=e.environment
    JOIN recharge_orders o ON o.id=f.order_id AND e.source_order_id=o.id::text
    WHERE d.state='failed' AND d.reason='invalid_website_context' AND d.attempts=0
      AND d.lease_token IS NULL AND d.deadline_at>statement_timestamp()
      AND NOT EXISTS (SELECT 1 FROM conversion.delivery_attempts a WHERE a.delivery_id=d.id)
      AND e.payment_policy_state='pending' AND e.payment_app_scope='reader'
      AND e.event_name='Purchase' AND e.action_source='website' AND e.time_source='paypal_subscription_ledger'
      AND e.occurred_at<=statement_timestamp() AND e.amount>=0 AND e.currency ~ '^[A-Z]{3}$'
      AND jsonb_typeof(e.matching_data->'external_id')='array' AND e.matching_data->'external_id'<>'[]'::jsonb
      AND f.event_name='Purchase' AND f.normalized_at IS NOT NULL AND f.original_send_started_at IS NULL
      AND f.snapshot->>'payment_policy_version'='1' AND f.snapshot->>'order_type'='subscription'
      AND NULLIF(f.snapshot->>'payment_kind','') IS NULL AND f.snapshot->>'user_id'=o.user_id::text
      AND f.snapshot->>'payment_key'=o.id::text AND o.order_type='subscription' AND o.fulfilled_at IS NOT NULL
      AND o.amount_cents>0 AND o.status IN ('Success','Paid','RefundPending','Refunded')
      AND o.capi_expectation->>'environment'=e.environment AND o.capi_expectation->>'payment_key'=o.id::text
      AND NOT EXISTS (
          SELECT 1 FROM conversion.payment_policy_decisions p WHERE p.environment=e.environment
            AND p.integration_id=e.integration_id AND p.app_scope=e.payment_app_scope AND p.payment_key=e.source_order_id)
      AND reader_verified_subscription_payment_kind(e.environment,o.id,o.user_id) IS DISTINCT FROM 'subscription_initial'
), reopened AS (
    UPDATE conversion.deliveries d
    SET state='blocked',reason='rule_fact_missing',finalized_at=NULL
    FROM candidates c WHERE d.id=c.id
    RETURNING d.event_id
)
UPDATE conversion.events e SET reason='rule_fact_missing'
WHERE e.id IN (SELECT event_id FROM reopened);

INSERT INTO reader_schema_migrations (version) VALUES (2026111915);
