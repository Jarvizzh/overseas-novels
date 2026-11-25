-- Preserve explicit Reader event selections. Untouched Reader integrations used
-- the built-in four-event default before this migration.
UPDATE conversion.integrations i
SET allowed_events=ARRAY['CompleteRegistration','InitiateCheckout','Purchase','Subscribe']
WHERE i.source='starnovel' AND i.account_key='reader' AND cardinality(i.allowed_events)=0
  AND NOT EXISTS (
    SELECT 1 FROM conversion.operation_audit a
    WHERE a.environment=i.environment AND a.action='configure_integration' AND a.subject_id=i.id::text
      AND a.after_state ? 'allowed_events'
  );

ALTER TABLE conversion.destinations
    ADD COLUMN consecutive_payment_claims integer NOT NULL DEFAULT 0;

CREATE TABLE conversion.collector_samples (
    environment text PRIMARY KEY,
    sampled_at timestamptz NOT NULL
);

CREATE INDEX conversion_delivery_recent_failures
    ON conversion.deliveries (environment,finalized_at) WHERE state='failed';
CREATE INDEX conversion_attempt_recent_limits
    ON conversion.delivery_attempts (responded_at) WHERE reason='meta_rate_limited';

-- Return only the time of matching frozen attribution. Renewal orders inherit
-- the attribution from checkout or the agreement, whose time predates the sale.
CREATE FUNCTION conversion.reader_payment_attribution_time(target_order bigint, pixel text)
RETURNS timestamptz LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, public, pg_temp AS $$
    SELECT LEAST(o.created_at,
        CASE WHEN s.attribution->>'pixel_id'=pixel THEN
            LEAST(s.created_at, CASE WHEN pp.attribution->>'pixel_id'=pixel THEN pp.submitted_at END)
        END,
        CASE WHEN v.attribution->>'pixel_id'=pixel THEN v.created_at END)
    FROM public.recharge_orders o
    LEFT JOIN public.user_subscriptions s ON s.subscription_id=o.subscription_id AND s.user_id=o.user_id
    LEFT JOIN public.paypal_subscription_intents pp ON pp.intent_id=s.checkout_intent_id AND pp.user_id=o.user_id
    LEFT JOIN public.payment_stripe_invoice_periods p ON p.order_id=o.id
    LEFT JOIN public.payment_stripe_subscription_intents v ON v.id=p.intent_id AND v.user_id=o.user_id
    WHERE o.id=target_order AND o.fb_lead_metadata->>'pixel_id'=pixel
$$;
REVOKE ALL ON FUNCTION conversion.reader_payment_attribution_time(bigint,text) FROM PUBLIC;

-- Order reconciliation reads a classification, without access to channel
-- credentials or raw receipts.
CREATE FUNCTION conversion.reader_order_environment(target_order bigint, frozen text, checkout text)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, public, pg_temp AS $$
    SELECT CASE WHEN NULLIF(frozen,'') IS NOT NULL THEN
             CASE WHEN frozen='live' THEN 'production' ELSE frozen END
         WHEN evidence.environments>1 OR (
             checkout IN ('live','production','sandbox') AND evidence.environments>0
             AND evidence.environment IS DISTINCT FROM CASE WHEN checkout='production' THEN 'live' ELSE checkout END
         ) THEN 'conflict'
         WHEN checkout IN ('live','production') OR evidence.environment='live' THEN 'production'
         WHEN checkout='sandbox' OR evidence.environment='sandbox' THEN 'sandbox'
         ELSE 'unknown' END
    FROM LATERAL (
        SELECT count(DISTINCT a.environment) AS environments,min(a.environment) AS environment
        FROM public.third_party_payment_orders r
        JOIN public.payment_channel_accounts a ON a.provider=r.payment_provider AND a.id=r.account_id
        WHERE NULLIF(frozen,'') IS NULL AND r.order_id=target_order AND r.status='VERIFIED'
    ) evidence
$$;
REVOKE ALL ON FUNCTION conversion.reader_order_environment(bigint,text,text) FROM PUBLIC;

INSERT INTO reader_schema_migrations(version) VALUES (2026111931);
