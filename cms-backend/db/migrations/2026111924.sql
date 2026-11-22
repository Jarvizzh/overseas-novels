-- Extend newly-created single checkouts to snapshot Managed Payments terms.
ALTER TABLE payment_stripe_one_time_offers
    ADD COLUMN sales_mode TEXT NOT NULL DEFAULT 'direct' CHECK (sales_mode IN ('direct','merchant_of_record')),
    ADD COLUMN tax_liability_account TEXT,
    ADD CONSTRAINT stripe_one_time_tax_liability CHECK (
        (sales_mode='direct' AND tax_liability_account IS NULL) OR
        (sales_mode='merchant_of_record' AND tax_liability_account IS NOT NULL AND tax_liability_account ~ '^acct_[a-zA-Z0-9]+$'));
ALTER TABLE payment_stripe_checkout_terms
    ADD COLUMN tax_liability_account TEXT,
    ADD COLUMN observed_tax_liability_account TEXT,
    ADD COLUMN observed_customer_tax_status TEXT;

CREATE TABLE payment_stripe_vip_offers (
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    offer_version_id INT NOT NULL REFERENCES payment_vip_offer_versions(slot_id),
    product_ref TEXT NOT NULL CHECK (product_ref ~ '^prod_[a-zA-Z0-9]+$'),
    price_ref TEXT NOT NULL CHECK (price_ref ~ '^price_[a-zA-Z0-9]+$'),
    sales_mode TEXT NOT NULL CHECK (sales_mode IN ('direct','merchant_of_record')),
    allowed_countries TEXT[] NOT NULL CHECK (cardinality(allowed_countries)>0),
    -- For Managed Payments, finance pins the actual tax-liability account
    -- supplied by the scoped Stripe Invoice. A missing match requires review.
    tax_liability_account TEXT,
    enabled BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY(account_id,offer_version_id),
    CHECK ((sales_mode='direct' AND tax_liability_account IS NULL) OR
           (sales_mode='merchant_of_record' AND tax_liability_account IS NOT NULL AND tax_liability_account ~ '^acct_[a-zA-Z0-9]+$'))
);

CREATE TABLE payment_stripe_subscription_intents (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    checkout_environment TEXT NOT NULL,
    offer_version_id INT NOT NULL REFERENCES payment_vip_offer_versions(slot_id),
    slot_id INT NOT NULL,
    template_id INT NOT NULL,
    product_ref TEXT NOT NULL,
    price_ref TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor>0),
    cycle TEXT NOT NULL CHECK (cycle IN ('day','week','month')),
    sales_mode TEXT NOT NULL CHECK (sales_mode IN ('direct','merchant_of_record')),
    allowed_countries TEXT[] NOT NULL,
    tax_liability_account TEXT,
    return_url TEXT NOT NULL,
    cancel_url TEXT NOT NULL,
    return_path TEXT NOT NULL DEFAULT '',
    attribution JSONB NOT NULL DEFAULT '{}',
    checkout_ref TEXT,
    approval_url TEXT,
    subscription_ref TEXT,
    customer_ref TEXT,
    status TEXT NOT NULL DEFAULT 'creating' CHECK (status IN ('creating','buyer_action','bound','closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_sent_at TIMESTAMPTZ,
    retry_until TIMESTAMPTZ,
    next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    invoice_scan_cursor TEXT,
    cancel_key UUID,
    cancel_first_sent_at TIMESTAMPTZ,
    cancel_retry_until TIMESTAMPTZ,
    cancel_next_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    UNIQUE(account_id,checkout_ref),
    UNIQUE(account_id,subscription_ref)
);
CREATE UNIQUE INDEX idx_stripe_vip_pending_user ON payment_stripe_subscription_intents(user_id)
    WHERE status IN ('creating','buyer_action');
CREATE INDEX idx_stripe_vip_due ON payment_stripe_subscription_intents(account_id,next_check_at,id)
    WHERE status IN ('creating','buyer_action','bound');

CREATE TABLE payment_stripe_invoice_periods (
    account_id BIGINT NOT NULL REFERENCES payment_channel_accounts(id),
    invoice_ref TEXT NOT NULL,
    intent_id UUID NOT NULL REFERENCES payment_stripe_subscription_intents(id),
    order_id BIGINT NOT NULL UNIQUE REFERENCES recharge_orders(id),
    service_start TIMESTAMPTZ,
    service_end TIMESTAMPTZ,
    billed_minor BIGINT NOT NULL CHECK (billed_minor>=0),
    billing_reason TEXT NOT NULL,
    country TEXT,
    observed_tax_liability_account TEXT,
    observed_customer_tax_status TEXT,
    review_reason TEXT,
    -- Only one admitted invoice may grant a given period. A reissued
    -- invoice requires independent evidence connecting it to the original.
    admitted BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(account_id,invoice_ref),
    CHECK (NOT admitted OR (service_start IS NOT NULL AND service_end>service_start))
);
CREATE INDEX idx_stripe_vip_invoice_intent ON payment_stripe_invoice_periods(intent_id,service_start,order_id);
CREATE INDEX idx_stripe_vip_cancel_due ON payment_stripe_subscription_intents(account_id,cancel_next_at)
    WHERE cancel_next_at IS NOT NULL AND cancelled_at IS NULL;
CREATE UNIQUE INDEX idx_stripe_admitted_invoice_period ON payment_stripe_invoice_periods(intent_id,service_start,service_end)
    WHERE admitted;

CREATE TABLE payment_stripe_invoice_allocations (
    account_id BIGINT NOT NULL,
    invoice_ref TEXT NOT NULL,
    allocation_ref TEXT NOT NULL,
    receipt_id BIGINT NOT NULL REFERENCES third_party_payment_orders(id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor>0),
    PRIMARY KEY(account_id,allocation_ref),
    FOREIGN KEY(account_id,invoice_ref) REFERENCES payment_stripe_invoice_periods(account_id,invoice_ref)
);

-- Preserve the existing PayPal evidence checks and classify Stripe by its admitted invoice.
CREATE OR REPLACE FUNCTION reader_subscription_payment_kind(target_order bigint, event_environment text)
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
    WHERE o.id=target_order AND o.order_type='subscription' AND o.payment_method='paypal'
      AND NULLIF(event_environment,'') IS NOT NULL
      AND COALESCE(o.capi_expectation->>'environment',event_environment)=event_environment
    UNION ALL
    SELECT CASE p.billing_reason WHEN 'subscription_create' THEN 'subscription_initial'
                                 WHEN 'subscription_cycle' THEN 'subscription_renewal' END
    FROM public.payment_stripe_invoice_periods p JOIN public.recharge_orders o ON o.id=p.order_id
    WHERE p.order_id=target_order AND p.admitted AND o.fulfilled_at IS NOT NULL
      AND o.checkout_environment=event_environment AND o.payment_method='stripe'

$$;

INSERT INTO reader_schema_migrations(version) VALUES (2026111924);
