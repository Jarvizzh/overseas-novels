-- Star Novel PostgreSQL schema. Apply to an empty database in one transaction.
-- Functions can refer to relations defined later in this file.
SET LOCAL check_function_bodies = false;

CREATE SCHEMA conversion;

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE FUNCTION conversion.guard_promotion_pixel() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
DECLARE
    active boolean;
BEGIN
    IF NEW.fb_pixel_id IS NULL THEN RETURN NEW; END IF;
    IF TG_OP='UPDATE' AND NEW.fb_pixel_id IS NOT DISTINCT FROM OLD.fb_pixel_id THEN RETURN NEW; END IF;
    SELECT enabled AND retired_at IS NULL INTO active
    FROM conversion.destinations WHERE legacy_fb_pixel_id=NEW.fb_pixel_id FOR SHARE;
    IF FOUND AND NOT active THEN
        RAISE EXCEPTION 'Conversion destination cannot be bound' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION conversion.lock_active_admin(operator_id text) RETURNS TABLE(role character varying)
    LANGUAGE sql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
    SELECT a.role FROM public.admins a WHERE a.id=operator_id AND a.status=1 FOR SHARE
$$;

CREATE FUNCTION conversion.lock_active_admin_for_permission(operator_id text, required_permission text) RETURNS TABLE(role_code character varying)
    LANGUAGE sql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
    WITH locked AS (
        SELECT a.id, a.status AS admin_status, r.id AS role_id, r.code, r.status AS role_status, r.is_super_admin
        FROM public.admins a
        JOIN public.roles r ON r.id = a.role_id
        WHERE a.id = operator_id
        FOR SHARE OF a, r
    )
    SELECT l.code::varchar
    FROM locked l
    WHERE l.admin_status = 1
      AND l.role_status = 1
      AND (
          l.is_super_admin = TRUE
          OR EXISTS (
              SELECT 1
              FROM public.role_permissions rp
              WHERE rp.role_id = l.role_id
                AND rp.permission_code = required_permission
          )
      )
$$;

CREATE FUNCTION conversion.lock_active_super_admin(operator_id text) RETURNS TABLE(role_code character varying)
    LANGUAGE sql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
    WITH locked AS (
        SELECT a.status AS admin_status, r.code, r.status AS role_status, r.is_super_admin
        FROM public.admins a
        JOIN public.roles r ON r.id = a.role_id
        WHERE a.id = operator_id
        FOR SHARE OF a, r
    )
    SELECT l.code::varchar
    FROM locked l
    WHERE l.admin_status = 1
      AND l.role_status = 1
      AND l.is_super_admin = TRUE
$$;

CREATE FUNCTION conversion.lock_repair_order(order_id bigint) RETURNS TABLE(capi_expectation jsonb, subscription_id character varying)
    LANGUAGE sql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
    SELECT o.capi_expectation,o.subscription_id FROM public.recharge_orders o WHERE o.id=order_id FOR UPDATE
$$;

CREATE FUNCTION conversion.mark_original_send_started() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE reader_conversion_repair_facts f SET original_send_started_at=NEW.sending_at
    FROM conversion.deliveries d JOIN conversion.events e ON e.id=d.event_id
    JOIN conversion.integrations i ON i.id=e.integration_id
    WHERE d.id=NEW.delivery_id AND d.role='original' AND i.source='starnovel' AND i.account_key='reader'
      AND f.environment=e.environment AND f.business_key=e.source_event_id AND f.event_name=e.source_event_type
      AND f.original_send_started_at IS NULL;
    RETURN NULL;
END $$;

CREATE FUNCTION conversion.reader_order_environment(target_order bigint, frozen text, checkout text) RETURNS text
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    AS $$
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

CREATE FUNCTION conversion.reader_payment_attribution_time(target_order bigint, pixel text) RETURNS timestamp with time zone
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    AS $$
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

CREATE FUNCTION conversion.require_complete_payment_policy_decision() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM conversion.payment_policy_decisions WHERE id = NEW.id AND outcome IS NULL) THEN
        RAISE EXCEPTION 'unfinished payment policy decision %', NEW.id USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END $$;

CREATE FUNCTION conversion.sinan_route_issue(event_key bigint, destination_key bigint) RETURNS text
    LANGUAGE sql STABLE
    AS $$
    SELECT CASE
        WHEN i.environment <> 'test' OR NOT i.enabled OR i.contract_version <> 'sinan-mock-v1'
          OR NOT (e.source_event_type = ANY(i.allowed_events))
          OR NOT COALESCE(e.partner_fields->>'app_id' = ANY(i.allowed_apps),false)
          OR NOT COALESCE((i.report_config->'app_pixels'->(e.partner_fields->>'app_id')) ? x.pixel_id,false)
          OR NOT COALESCE((i.report_config->'app_pixels'->(e.partner_fields->>'app_id')) ? (e.partner_fields->>'pixel_id'),false)
          OR NOT EXISTS (
              SELECT 1 FROM conversion.integration_destinations r
              WHERE r.integration_id=i.id AND r.destination_id=x.id
          )
          OR NOT EXISTS (
              SELECT 1 FROM conversion.integration_destinations r
              JOIN conversion.destinations origin ON origin.id=r.destination_id
              WHERE r.integration_id=i.id AND origin.pixel_id=e.partner_fields->>'pixel_id' AND origin.media='meta'
          )
        THEN 'partner_route_not_authorized'
        WHEN NULLIF(i.report_config->'promotion_pixels'->(e.partner_fields->>'app_id')->>e.promotion_id,'')
               <> e.partner_fields->>'pixel_id'
        THEN 'promotion_pixel_mismatch'
        WHEN NOT x.enabled OR x.retired_at IS NOT NULL THEN 'destination_disabled'
        ELSE ''
    END
    FROM conversion.events e JOIN conversion.integrations i ON i.id=e.integration_id
    JOIN conversion.destinations x ON x.id=destination_key AND x.environment=e.environment
    WHERE e.id=event_key AND i.source='sinan'
$$;

CREATE FUNCTION conversion.stamp_inbox_processed_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.state='pending' THEN
        NEW.processed_at := NULL;
    ELSIF TG_OP='INSERT' THEN
        NEW.processed_at := clock_timestamp();
    ELSIF OLD.state='pending' OR OLD.state<>NEW.state THEN
        NEW.processed_at := clock_timestamp();
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.admin_has_permission(p_admin_id text, p_permission text) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
    SELECT EXISTS (
        SELECT 1
        FROM public.admins a
        JOIN public.roles r ON r.id = a.role_id
        WHERE a.id = p_admin_id
          AND a.status = 1
          AND r.status = 1
          AND (
              r.is_super_admin = TRUE
              OR EXISTS (
                  SELECT 1
                  FROM public.role_permissions rp
                  WHERE rp.role_id = r.id
                    AND rp.permission_code = p_permission
              )
          )
    )
$$;

CREATE FUNCTION public.advance_reader_body_revision() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF (NEW.content_object_key, NEW.content_sha256, NEW.content)
        IS DISTINCT FROM (OLD.content_object_key, OLD.content_sha256, OLD.content) THEN
        NEW.body_revision := OLD.body_revision + 1;
        IF NEW.chapter_index IS NOT NULL AND NEW.content_object_key IS NULL
            AND NEW.content IS DISTINCT FROM OLD.content
            AND (NEW.content_object_key, NEW.content_sha256)
                IS NOT DISTINCT FROM (OLD.content_object_key, OLD.content_sha256) THEN
            NEW.content_sha256 := NULL;
            NEW.content_preview := NULL;
        END IF;
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.assert_paypal_identity(cid text, mode text) RETURNS void
    LANGUAGE plpgsql
    AS $$
DECLARE environment_name TEXT;
BEGIN
 IF cid='' THEN RAISE EXCEPTION 'PayPal client ID required'; END IF;
 environment_name:=CASE WHEN mode IN ('live','production') THEN 'live' ELSE 'sandbox' END;
 INSERT INTO paypal_identity_lock(id,client_id,environment) VALUES(true,cid,environment_name) ON CONFLICT DO NOTHING;
 IF NOT EXISTS(SELECT 1 FROM paypal_identity_lock WHERE id AND client_id=cid AND environment=environment_name) THEN
 RAISE EXCEPTION 'PayPal merchant/environment change requires an audited migration'; END IF;
END $$;

CREATE FUNCTION public.bump_reader_chapter_statement() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    -- A CMS publication updates the novel's word count and generation together.
    IF current_setting('star_novel.publishing_chapters', true) = 'on' THEN
        RETURN NULL;
    END IF;
    IF TG_OP = 'INSERT' THEN
        UPDATE novels SET content_version = content_version + 1
        WHERE id IN (SELECT novel_id FROM new_chapters WHERE chapter_index IS NOT NULL);
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE novels SET content_version = content_version + 1
        WHERE id IN (SELECT novel_id FROM old_chapters WHERE chapter_index IS NOT NULL);
    ELSE
        UPDATE novels SET content_version = content_version + 1 WHERE id IN (
            SELECT DISTINCT unnest(ARRAY[o.novel_id, n.novel_id])
            FROM old_chapters o FULL JOIN new_chapters n USING (id)
            WHERE (o.chapter_index IS NOT NULL OR n.chapter_index IS NOT NULL)
              AND (o.novel_id, o.chapter_index, o.title, o.word_count, o.body_revision)
                  IS DISTINCT FROM (n.novel_id, n.chapter_index, n.title, n.word_count, n.body_revision)
        );
    END IF;
    RETURN NULL;
END $$;

CREATE FUNCTION public.bump_reader_chapter_version() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP<>'INSERT' THEN UPDATE novels SET content_version=content_version WHERE id=OLD.novel_id; END IF;
 IF TG_OP='INSERT' OR (TG_OP='UPDATE' AND NEW.novel_id<>OLD.novel_id) THEN UPDATE novels SET content_version=content_version WHERE id=NEW.novel_id; END IF;
 RETURN NULL;
END $$;

CREATE FUNCTION public.bump_reader_novel_version() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.content_version IS DISTINCT FROM OLD.content_version
        OR (NEW.status, NEW.publish_status) IS DISTINCT FROM (OLD.status, OLD.publish_status) THEN
        NEW.content_version := OLD.content_version + 1;
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.complete_paypal_refund(target_order bigint, provider_refund text) RETURNS void
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.discard_unfulfilled_legacy_conversion() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
BEGIN
    DELETE FROM public.payment_event_outbox WHERE order_id=NEW.id AND delivered_at IS NULL;
    RETURN NULL;
END $$;

CREATE FUNCTION public.forbid_active_product_binding_delete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.external_product_ref <> ''
       OR OLD.external_price_ref <> ''
       OR OLD.enabled = true
       OR OLD.review_required = true
       OR OLD.validation_status IN ('verified', 'review', 'pending') THEN
        RAISE EXCEPTION 'cannot delete active, verified, or externally provisioned product_channel_bindings row';
    END IF;
    RETURN OLD;
END;
$$;

CREATE FUNCTION public.forbid_non_draft_publication_delete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF OLD.status <> 'draft'
       OR OLD.published_at IS NOT NULL
       OR OLD.paused_at IS NOT NULL
       OR OLD.retired_at IS NOT NULL THEN
        RAISE EXCEPTION 'cannot delete non-draft or previously published product_publications row';
    END IF;
    RETURN OLD;
END;
$$;

CREATE FUNCTION public.forbid_product_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.id <> OLD.id
           OR NEW.source_product_id IS DISTINCT FROM OLD.source_product_id
           OR NEW.currency <> OLD.currency
           OR NEW.created_by IS DISTINCT FROM OLD.created_by
           OR NEW.created_at <> OLD.created_at THEN
            RAISE EXCEPTION 'immutable product metadata columns cannot be modified';
        END IF;
        IF NOT is_product_mutable_draft(OLD.id) THEN
            RAISE EXCEPTION 'products rows are immutable once bound, published, or referenced; create a copied version instead';
        END IF;
        RETURN NEW;
    ELSIF TG_OP = 'DELETE' THEN
        IF NOT is_product_mutable_draft(OLD.id) THEN
            RAISE EXCEPTION 'products rows cannot be deleted once bound, published, or referenced';
        END IF;
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'products rows are immutable; create a copied version instead';
END;
$$;

CREATE FUNCTION public.guard_paypal_identity_settings() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF COALESCE(NEW.key,OLD.key) IN ('paypal_client_id','paypal_mode','payment_sandbox_mode') AND EXISTS(SELECT 1 FROM paypal_identity_lock) THEN
  IF TG_OP='INSERT' AND EXISTS(SELECT 1 FROM system_configs WHERE key=NEW.key AND value=NEW.value) THEN RETURN NEW; END IF;
  IF TG_OP='INSERT' OR TG_OP='DELETE' THEN RAISE EXCEPTION 'PayPal identity settings are locked'; END IF;
  IF NEW.value IS DISTINCT FROM OLD.value THEN RAISE EXCEPTION 'PayPal identity settings are locked'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;

CREATE FUNCTION public.guard_unlock_chapter_identity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.chapter_id IS NULL OR NULLIF(btrim(NEW.identity_evidence), '') IS NULL
       OR NEW.identity_resolved_at IS NULL THEN
        RAISE EXCEPTION 'chapter identity and resolution evidence are required';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.chapter_id IS NOT NULL
       AND (NEW.chapter_id, NEW.novel_id, NEW.user_id, NEW.identity_evidence, NEW.identity_resolved_at)
           IS DISTINCT FROM
           (OLD.chapter_id, OLD.novel_id, OLD.user_id, OLD.identity_evidence, OLD.identity_resolved_at) THEN
        RAISE EXCEPTION 'resolved chapter purchase identity cannot be changed';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.is_product_mutable_draft(p_product_id bigint) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
    SELECT
        NOT EXISTS (
            SELECT 1 FROM product_publications
            WHERE product_id = p_product_id
              AND (status <> 'draft' OR published_at IS NOT NULL OR paused_at IS NOT NULL OR retired_at IS NOT NULL)
        )
        AND NOT EXISTS (
            SELECT 1 FROM product_channel_bindings
            WHERE product_id = p_product_id
              AND (
                  external_product_ref <> ''
                  OR external_price_ref <> ''
                  OR enabled = true
                  OR review_required = true
                  OR validation_status IN ('verified', 'review', 'pending')
              )
        )
        AND NOT EXISTS (SELECT 1 FROM recharge_slots WHERE product_id = p_product_id)
        AND NOT EXISTS (SELECT 1 FROM products WHERE source_product_id = p_product_id)
        AND NOT EXISTS (SELECT 1 FROM recharge_orders WHERE product_id = p_product_id)
        AND NOT EXISTS (SELECT 1 FROM user_subscriptions WHERE product_id = p_product_id)
        AND NOT EXISTS (SELECT 1 FROM paypal_subscription_intents WHERE product_id = p_product_id)
        AND NOT EXISTS (SELECT 1 FROM payment_vip_checkout_intents WHERE product_id = p_product_id)
        AND NOT EXISTS (SELECT 1 FROM payment_stripe_subscription_intents WHERE product_id = p_product_id)
        AND NOT EXISTS (SELECT 1 FROM payment_checkout_requests WHERE product_id = p_product_id);
$$;

CREATE FUNCTION public.lock_reader_first_payment() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.mark_cancel_requested() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF NEW.status='CANCEL_PENDING' AND OLD.status IS DISTINCT FROM NEW.status THEN NEW.cancel_requested_at=now(); END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION public.payment_last_stripe_billing_country(target_account bigint, target_user bigint) RETURNS text
    LANGUAGE sql STABLE
    AS $_$
    SELECT t.payer_country FROM third_party_payment_orders t
    JOIN recharge_orders o ON o.id=t.order_id
    JOIN payment_fulfillment_funding f ON f.order_id=o.id AND f.receipt_id=t.id
    WHERE t.account_id=target_account AND o.user_id=target_user AND t.payment_provider='stripe'
      AND o.fulfilled_at IS NOT NULL AND t.status='VERIFIED' AND t.payer_country ~ '^[A-Z]{2}$'
    ORDER BY o.paid_at DESC NULLS LAST,t.id DESC LIMIT 1
$_$;

CREATE SEQUENCE public.recharge_orders_id_seq
    START WITH 10000
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

SET default_tablespace = '';

SET default_table_access_method = heap;

CREATE TABLE public.recharge_orders (
    id bigint DEFAULT nextval('public.recharge_orders_id_seq'::regclass) NOT NULL,
    user_id bigint NOT NULL,
    external_ref_id character varying(100),
    amount_cents integer NOT NULL,
    currency character varying(3) DEFAULT 'USD'::character varying,
    coins integer DEFAULT 0,
    bonus_coins_credited integer DEFAULT 0,
    payment_method character varying(20) NOT NULL,
    status character varying(20) DEFAULT 'Pending'::character varying,
    utm_source character varying(100),
    utm_campaign character varying(100),
    fb_lead_metadata jsonb,
    paid_at timestamp with time zone,
    order_type character varying(20) DEFAULT 'single'::character varying,
    subscription_id character varying(100),
    recharge_template_id integer,
    recharge_slot_id integer,
    promotion_link_id integer,
    novel_id integer,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    checkout_id uuid,
    fulfilled_at timestamp with time zone,
    paid_period_start timestamp with time zone,
    paid_period_end timestamp with time zone,
    paypal_refund_reported boolean DEFAULT false NOT NULL,
    reconciliation_at timestamp with time zone,
    reconciliation_note text,
    payment_events_enabled boolean DEFAULT false NOT NULL,
    reconciliation_attempts integer DEFAULT 0 NOT NULL,
    reconciliation_error text,
    capi_expectation jsonb,
    checkout_environment text,
    checkout_return_path text DEFAULT ''::text NOT NULL,
    product_id bigint,
    product_snapshot jsonb
);

CREATE FUNCTION public.payment_order_environment(o public.recharge_orders) RETURNS text
    LANGUAGE sql STABLE
    AS $$
    SELECT CASE WHEN count(DISTINCT a.environment)>1 THEN NULL
        ELSE COALESCE(min(a.environment),CASE o.checkout_environment WHEN 'production' THEN 'live' WHEN 'sandbox' THEN 'sandbox' END)
        END
    FROM third_party_payment_orders p JOIN payment_channel_accounts a ON a.id=p.account_id
    WHERE p.order_id=o.id AND p.status IN ('VERIFIED','COMPLETED')
$$;

CREATE TABLE public.user_subscriptions (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    subscription_id character varying(100) NOT NULL,
    plan_id character varying(100),
    slot_id integer,
    template_id integer,
    status character varying(30) DEFAULT 'ACTIVE'::character varying NOT NULL,
    cycle character varying(20) DEFAULT 'month'::character varying NOT NULL,
    price_cents integer DEFAULT 0 NOT NULL,
    currency character varying(10) DEFAULT 'USD'::character varying,
    payment_method character varying(30) DEFAULT 'paypal'::character varying,
    current_period_start timestamp with time zone,
    current_period_end timestamp with time zone,
    next_billing_time timestamp with time zone,
    last_payment_time timestamp with time zone,
    cancelled_at timestamp with time zone,
    raw_payload jsonb,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    paypal_transaction_cursor timestamp with time zone,
    paypal_next_check_at timestamp with time zone,
    cancel_next_attempt_at timestamp with time zone,
    cancel_attempts integer DEFAULT 0 NOT NULL,
    cancel_reason text,
    cancel_last_error text,
    attribution jsonb DEFAULT '{}'::jsonb NOT NULL,
    paypal_recovery_requested boolean DEFAULT false NOT NULL,
    paypal_check_attempts integer DEFAULT 0 NOT NULL,
    paypal_check_error text,
    cancel_requested_at timestamp with time zone,
    paypal_status_changed_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    capi_subscribe_state text DEFAULT 'unknown'::text NOT NULL,
    capi_subscribe_order_id bigint,
    checkout_account_id bigint,
    checkout_intent_id uuid,
    checkout_return_path text DEFAULT ''::text NOT NULL,
    paypal_activation_requested_at timestamp with time zone,
    vip_offer_version_id integer,
    paypal_renewal_failed_at timestamp with time zone,
    product_id bigint,
    product_snapshot jsonb,
    CONSTRAINT user_subscriptions_capi_subscribe_state_check CHECK ((capi_subscribe_state = ANY (ARRAY['eligible'::text, 'claimed'::text, 'historical'::text, 'unknown'::text]))),
    CONSTRAINT user_subscriptions_check CHECK (((capi_subscribe_state = 'claimed'::text) = (capi_subscribe_order_id IS NOT NULL)))
);

CREATE FUNCTION public.payment_subscription_environment(s public.user_subscriptions) RETURNS text
    LANGUAGE sql STABLE
    AS $$
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

CREATE FUNCTION public.reader_chapter_preview(body text) RETURNS text
    LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
    AS $$
    SELECT CASE WHEN body = '' THEN ''
        ELSE left(body, LEAST(250, char_length(body) / 2)) || '...' END
$$;

CREATE FUNCTION public.reader_has_chapter_unlock(reader_id bigint, target_id character varying) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
    WITH RECURSIVE sources(id, cutoff) AS (
        SELECT target_id::varchar(64), 'infinity'::timestamptz
        UNION ALL
        SELECT l.source_chapter_id, LEAST(s.cutoff, l.purchased_before)
        FROM sources s JOIN chapter_entitlement_links l ON l.chapter_id = s.id
    )
    SELECT EXISTS (
        SELECT 1 FROM sources s JOIN unlock_records u ON u.chapter_id = s.id
        WHERE u.user_id = reader_id AND u.created_at <= s.cutoff
    );
$$;

CREATE FUNCTION public.reader_payment_coverage_verified(event_environment text, link integer) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
    SELECT EXISTS (
        SELECT 1 FROM public.promotion_links p
        WHERE p.id=link AND (
            p.created_at >= (SELECT applied_at FROM public.reader_schema_migrations WHERE version=2026111911)
            OR EXISTS (
                SELECT 1 FROM public.reader_payment_promotion_coverage c
                WHERE c.environment=event_environment AND c.promotion_link_id=link)))
$$;

CREATE FUNCTION public.reader_payment_promotion_key(link_id integer, attribution jsonb) RETURNS integer
    LANGUAGE sql IMMUTABLE
    AS $_$
    SELECT CASE
        WHEN attribution->>'promotion_link_id' ~ '^[1-9][0-9]{0,9}$' THEN
            CASE WHEN (attribution->>'promotion_link_id')::bigint <= 2147483647
                 THEN (attribution->>'promotion_link_id')::integer ELSE link_id END
        ELSE link_id END
$_$;

CREATE TABLE public.promotion_links (
    id integer NOT NULL,
    name character varying(255) DEFAULT ''::character varying,
    novel_id integer NOT NULL,
    novel_title character varying(255) NOT NULL,
    chapter_index integer,
    utm_source character varying(100),
    utm_campaign character varying(100),
    generated_url text NOT NULL,
    fb_pixel_id integer,
    recharge_template_id integer,
    domain_id integer,
    domain character varying(255) DEFAULT ''::character varying,
    coin_cost_per_thousand integer,
    start_pay_chapter_index integer,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    environment text DEFAULT 'live'::text NOT NULL,
    CONSTRAINT promotion_links_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text])))
);

CREATE FUNCTION public.reader_promotion_for_user(uid bigint) RETURNS SETOF public.promotion_links
    LANGUAGE sql STABLE
    AS $$
 SELECT * FROM reader_promotion_for_user_env(uid, 'live')
$$;

CREATE FUNCTION public.reader_promotion_for_user_env(uid bigint, env text) RETURNS SETOF public.promotion_links
    LANGUAGE sql STABLE
    AS $$
 SELECT p.*
 FROM users u
 JOIN promotion_links p
   ON p.id = COALESCE(
       u.promotion_link_id,
       CASE WHEN NOT u.promotion_resolved THEN resolve_reader_promotion_for_env(0, u.utm_source, u.utm_campaign, env) END
   )
 WHERE u.id = uid
   AND (p.environment = 'live' OR (env = 'sandbox' AND p.environment = 'sandbox'))
$$;

CREATE FUNCTION public.reader_subscription_payment_kind(target_order bigint) RETURNS text
    LANGUAGE sql STABLE
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
    SELECT public.reader_subscription_payment_kind(o.id,o.capi_expectation->>'environment')
    FROM public.recharge_orders o WHERE o.id=target_order
$$;

CREATE FUNCTION public.reader_subscription_payment_kind(target_order bigint, event_environment text) RETURNS text
    LANGUAGE sql STABLE
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
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

CREATE FUNCTION public.reader_verified_first_payment(event_environment text, target_order bigint, target_user bigint, target_promotion integer) RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
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

CREATE FUNCTION public.reader_verified_subscription_payment_kind(event_environment text, target_order bigint, target_user bigint) RETURNS text
    LANGUAGE sql STABLE SECURITY DEFINER
    SET search_path TO 'pg_catalog', 'pg_temp'
    AS $$
    SELECT public.reader_subscription_payment_kind(o.id,event_environment)
    FROM public.recharge_orders o
    WHERE o.id=target_order AND o.user_id=target_user AND o.order_type='subscription'
      AND o.capi_expectation->>'environment'=event_environment AND o.capi_expectation->>'payment_key'=o.id::text
      AND o.fulfilled_at IS NOT NULL AND o.amount_cents>0 AND o.status IN ('Success','Paid','RefundPending','Refunded')
$$;

CREATE FUNCTION public.record_paypal_channel_account() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.client_id NOT IN ('', 'paypal_client_id_placeholder') THEN
        INSERT INTO payment_channel_accounts (provider, environment, account_key, credentials_ref, webhook_ref)
        VALUES ('paypal', NEW.environment, NEW.client_id, 'legacy:paypal', 'env:PAYPAL_WEBHOOK_ID')
        ON CONFLICT (provider, environment, account_key) DO NOTHING;
    END IF;
    RETURN NEW;
END
$$;

CREATE FUNCTION public.record_reader_payment_conversion(target_order bigint, original_time timestamp with time zone, time_source text, event_environment text, include_subscribe boolean, automatic_payment boolean) RETURNS void
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.record_reader_payment_conversion(target_order bigint, original_time timestamp with time zone, time_source text, event_environment text, include_subscribe boolean, automatic_payment boolean, subscription_kind text) RETURNS void
    LANGUAGE plpgsql
    AS $$
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

CREATE FUNCTION public.resolve_reader_promotion(link integer, source text, campaign text) RETURNS integer
    LANGUAGE sql STABLE
    AS $$
 SELECT resolve_reader_promotion_for_env(link, source, campaign, 'live')
$$;

CREATE FUNCTION public.resolve_reader_promotion_for_env(link integer, source text, campaign text, env text) RETURNS integer
    LANGUAGE sql STABLE
    AS $$
 SELECT CASE WHEN link > 0 THEN (
     SELECT p.id FROM promotion_links p
     JOIN novels n ON n.id = p.novel_id
     WHERE p.id = link
       AND (p.environment = 'live' OR (env = 'sandbox' AND p.environment = 'sandbox'))
       AND n.publish_status = 'published'
       AND n.status <> 'archived'
 )
 WHEN env = 'sandbox' AND source <> '' AND campaign <> '' AND EXISTS (
     SELECT 1 FROM promotion_links
     WHERE environment = 'sandbox' AND utm_source = source AND utm_campaign = campaign
 ) THEN (
     SELECT min(id) FROM promotion_links
     WHERE environment = 'sandbox'
       AND utm_source = source AND utm_campaign = campaign
     HAVING count(*) = 1
 )
 ELSE (
     SELECT min(id) FROM promotion_links
     WHERE environment = 'live'
       AND utm_source = source AND utm_campaign = campaign
       AND source <> '' AND campaign <> ''
     HAVING count(*) = 1
 ) END
$$;

CREATE FUNCTION public.save_paypal_refund_item(refund_ref text, capture_ref text, cents bigint, currency_code text) RETURNS void
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF refund_ref = '' THEN RAISE EXCEPTION 'Missing PayPal refund ID'; END IF;
    INSERT INTO paypal_refund_items(refund_id,capture_id,amount_cents,currency)
        VALUES(refund_ref,capture_ref,cents,currency_code) ON CONFLICT(refund_id) DO NOTHING;
    IF NOT EXISTS(SELECT 1 FROM paypal_refund_items
        WHERE refund_id=refund_ref AND capture_id=capture_ref AND amount_cents=cents AND currency=currency_code) THEN
        RAISE EXCEPTION 'Conflicting PayPal refund evidence';
    END IF;
END;
$$;

CREATE FUNCTION public.settle_paypal_refund_item(target_order bigint, refund_ref text, capture_ref text, cents bigint, currency_code text) RETURNS boolean
    LANGUAGE plpgsql
    AS $$
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

CREATE TABLE public.recharge_slots (
    id integer NOT NULL,
    template_id integer NOT NULL,
    slot_index integer NOT NULL,
    type character varying(20) DEFAULT ''::character varying,
    coins integer DEFAULT 0,
    bonus integer DEFAULT 0,
    vip_duration character varying(10) DEFAULT ''::character varying,
    subscription_cycle character varying(20) DEFAULT ''::character varying,
    vip_name character varying(50) DEFAULT ''::character varying,
    vip_desc character varying(255) DEFAULT ''::character varying,
    price character varying(20) DEFAULT ''::character varying,
    price_cents integer DEFAULT 0,
    vip_offer_version_id integer,
    product_id bigint NOT NULL,
    badge text DEFAULT ''::text NOT NULL,
    is_highlighted boolean DEFAULT false NOT NULL,
    display_alias text DEFAULT ''::text NOT NULL,
    list_price_cents integer DEFAULT 0 NOT NULL,
    CONSTRAINT recharge_slots_list_price_cents_check CHECK ((list_price_cents >= 0))
);

CREATE FUNCTION public.snapshot_vip_offer(s public.recharge_slots) RETURNS integer
    LANGUAGE sql
    AS $$
    INSERT INTO payment_vip_offer_versions (slot_id, template_id, slot_index, amount_minor, currency, cycle, name, description)
    SELECT s.id, s.template_id, s.slot_index, s.price_cents, 'USD', offer.cycle,
        COALESCE(NULLIF(s.vip_name, ''), 'Star Novel VIP ' || initcap(offer.cycle)),
        COALESCE(NULLIF(s.vip_desc, ''), 'Unlimited reading access for VIP members')
    FROM (SELECT COALESCE(NULLIF(s.subscription_cycle, ''), NULLIF(s.vip_duration, ''), 'month') AS cycle) offer
    WHERE s.type IN ('vip', 'subscription') AND s.price_cents > 0 AND offer.cycle IN ('day', 'week', 'month')
    ON CONFLICT (template_id, slot_index, amount_minor, currency, cycle, name, description)
        DO UPDATE SET name = EXCLUDED.name
    RETURNING slot_id
$$;

CREATE FUNCTION public.track_chapter_content_reference() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    previous_key TEXT;
    next_key TEXT;
BEGIN
    IF TG_OP <> 'INSERT' THEN previous_key := OLD.content_object_key; END IF;
    IF TG_OP <> 'DELETE' THEN next_key := NEW.content_object_key; END IF;
    IF TG_OP <> 'INSERT' AND previous_key IS NULL
       AND OLD.chapter_index IS NOT NULL AND OLD.content = ''
       AND (TG_OP = 'DELETE' OR NEW.chapter_index IS DISTINCT FROM OLD.chapter_index
            OR NEW.content_object_key IS NOT NULL OR NEW.content <> '') THEN
        INSERT INTO chapter_content_objects (object_key, chapter_id, sha256, state)
        VALUES (OLD.novel_id || '/' || OLD.chapter_index || '.txt', OLD.id, 'legacy', 'retired')
        ON CONFLICT (object_key) DO NOTHING;
    END IF;
    IF previous_key IS NOT DISTINCT FROM next_key THEN RETURN NULL; END IF;
    IF next_key IS NOT NULL THEN
        UPDATE chapter_content_objects SET state = 'published'
        WHERE object_key = next_key AND chapter_id = NEW.id AND sha256 = NEW.content_sha256
          AND state = 'staged' AND next_attempt_at > now();
        IF NOT FOUND THEN RAISE EXCEPTION 'chapter content is no longer staged'; END IF;
    END IF;
    IF previous_key IS NOT NULL THEN
        -- Keep objects long enough for readers using the previous committed version.
        UPDATE chapter_content_objects SET state = 'retired', next_attempt_at = now() + interval '1 day'
        WHERE object_key = previous_key AND state = 'published';
    END IF;
    RETURN NULL;
END;
$$;

CREATE FUNCTION public.track_payment_unattributed_count() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path TO 'pg_catalog', 'public', 'pg_temp'
    AS $$
DECLARE
    old_shard SMALLINT;
    new_shard SMALLINT;
    previous_attempts INTEGER;
    current_attempts INTEGER;
    remaining BIGINT;
BEGIN
    IF TG_OP <> 'INSERT' AND OLD.state = 'unattributed' THEN
        old_shard := (OLD.order_id % 32)::smallint;
        previous_attempts := OLD.attempts;
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.state = 'unattributed' THEN
        new_shard := (NEW.order_id % 32)::smallint;
        current_attempts := NEW.attempts;
    END IF;
    IF (old_shard, previous_attempts) IS NOT DISTINCT FROM (new_shard, current_attempts) THEN
        RETURN NULL;
    END IF;

    IF old_shard IS NOT NULL THEN
        UPDATE payment_unattributed_counts SET event_count = event_count - 1
        WHERE shard = old_shard AND attempts = previous_attempts RETURNING event_count INTO STRICT remaining;
        IF remaining = 0 THEN
            DELETE FROM payment_unattributed_counts WHERE shard = old_shard AND attempts = previous_attempts;
        END IF;
    END IF;
    IF new_shard IS NOT NULL THEN
        INSERT INTO payment_unattributed_counts (shard, attempts, event_count)
        VALUES (new_shard, current_attempts, 1)
        ON CONFLICT (shard, attempts) DO UPDATE
        SET event_count = payment_unattributed_counts.event_count + 1;
    END IF;
    RETURN NULL;
END $$;

CREATE FUNCTION public.track_paypal_subscription_status_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        NEW.paypal_status_changed_at := clock_timestamp();
    END IF;
    RETURN NEW;
END $$;

CREATE FUNCTION public.whole_book_offer_available(p_user_id bigint, p_template_id integer, p_novel_id bigint) RETURNS boolean
    LANGUAGE sql STABLE
    AS $$
 SELECT EXISTS(
  SELECT 1 FROM users u
  LEFT JOIN LATERAL(SELECT id, novel_id, recharge_template_id FROM reader_promotion_for_user(u.id)) p ON TRUE
  WHERE u.id = p_user_id AND p_novel_id > 0
  AND EXISTS(SELECT 1 FROM novels WHERE id = p_novel_id AND status <> 'archived' AND publish_status = 'published')
  AND p_template_id = COALESCE(
      p.recharge_template_id,
      (SELECT id FROM recharge_templates WHERE environment = 'live' ORDER BY is_default DESC NULLS LAST, id LIMIT 1)
  )
  AND (p.novel_id = p_novel_id OR (p.id IS NULL AND EXISTS(SELECT 1 FROM recharge_templates WHERE id = p_template_id AND is_default AND environment = 'live')))
 )
$$;

CREATE TABLE conversion.alerts (
    environment text NOT NULL,
    kind text NOT NULL,
    subject text NOT NULL,
    detail jsonb NOT NULL,
    opened_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    resolved_at timestamp with time zone
);

CREATE TABLE conversion.collector_samples (
    environment text NOT NULL,
    sampled_at timestamp with time zone NOT NULL
);

CREATE TABLE conversion.credentials (
    id bigint NOT NULL,
    ciphertext bytea NOT NULL,
    key_version text NOT NULL,
    rotated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE conversion.credentials ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.credentials_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.deliveries (
    id bigint NOT NULL,
    event_id bigint NOT NULL,
    destination_id bigint NOT NULL,
    environment text NOT NULL,
    meta_event_id text NOT NULL,
    role text NOT NULL,
    frozen_route jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deadline_at timestamp with time zone NOT NULL,
    finalized_at timestamp with time zone,
    state text DEFAULT 'pending'::text NOT NULL,
    reason text,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_attempt_at timestamp with time zone,
    lease_token uuid,
    lease_until timestamp with time zone,
    attempts integer DEFAULT 0 NOT NULL,
    retry_budget_base_attempt integer DEFAULT 0 NOT NULL,
    budget_reset_used boolean DEFAULT false NOT NULL,
    unknown_retry_used boolean DEFAULT false NOT NULL,
    unknown_attempt_no integer,
    max_batch_size integer DEFAULT 100 NOT NULL,
    CONSTRAINT deliveries_attempts_check CHECK ((attempts >= 0)),
    CONSTRAINT deliveries_check CHECK (((retry_budget_base_attempt >= 0) AND (retry_budget_base_attempt <= attempts))),
    CONSTRAINT deliveries_check1 CHECK (((unknown_attempt_no > 0) AND (unknown_attempt_no <= attempts))),
    CONSTRAINT deliveries_check2 CHECK (((lease_token IS NULL) = (lease_until IS NULL))),
    CONSTRAINT deliveries_check3 CHECK (((finalized_at IS NOT NULL) = (state = ANY (ARRAY['succeeded'::text, 'failed'::text, 'skipped'::text])))),
    CONSTRAINT deliveries_check4 CHECK (((lease_token IS NULL) OR (state = 'pending'::text))),
    CONSTRAINT deliveries_max_batch_size_check CHECK (((max_batch_size >= 1) AND (max_batch_size <= 100))),
    CONSTRAINT deliveries_meta_event_id_check CHECK ((meta_event_id <> ''::text)),
    CONSTRAINT deliveries_role_check CHECK ((role = ANY (ARRAY['original'::text, 'additional'::text]))),
    CONSTRAINT deliveries_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'blocked'::text, 'outcome_unknown'::text, 'succeeded'::text, 'failed'::text, 'skipped'::text])))
);

ALTER TABLE conversion.deliveries ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.deliveries_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.delivery_attempts (
    delivery_id bigint NOT NULL,
    attempt_no integer NOT NULL,
    lease_token uuid NOT NULL,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    sending_at timestamp with time zone,
    responded_at timestamp with time zone,
    recorded_at timestamp with time zone,
    outcome text,
    reason text,
    http_status integer,
    graph_code integer,
    graph_subcode integer,
    events_received integer,
    fbtrace_id text,
    batch_id bigint,
    batch_position integer,
    batch_confirmed boolean GENERATED ALWAYS AS (
CASE
    WHEN ((batch_id IS NOT NULL) AND (outcome = 'accepted'::text)) THEN true
    ELSE NULL::boolean
END) STORED,
    CONSTRAINT delivery_attempt_accepted_evidence CHECK (((outcome IS DISTINCT FROM 'accepted'::text) OR ((sending_at IS NOT NULL) AND (NOT (http_status IS DISTINCT FROM 200)) AND ((batch_id IS NOT NULL) OR (NOT (events_received IS DISTINCT FROM 1)))))),
    CONSTRAINT delivery_attempts_attempt_no_check CHECK ((attempt_no > 0)),
    CONSTRAINT delivery_attempts_check CHECK (((recorded_at IS NULL) = (outcome IS NULL))),
    CONSTRAINT delivery_attempts_check2 CHECK (((batch_position IS NULL) OR ((batch_id IS NOT NULL) AND ((batch_position >= 1) AND (batch_position <= 100)) AND (sending_at IS NOT NULL)))),
    CONSTRAINT delivery_attempts_check3 CHECK (((batch_id IS NULL) OR ((events_received IS NULL) AND ((sending_at IS NULL) = (batch_position IS NULL))))),
    CONSTRAINT delivery_attempts_outcome_check CHECK ((outcome = ANY (ARRAY['accepted'::text, 'not_sent'::text, 'retryable_rejection'::text, 'permanent_rejection'::text, 'outcome_unknown'::text])))
);

CREATE TABLE conversion.delivery_diagnostics (
    delivery_id bigint NOT NULL,
    attempt_no integer NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    late boolean NOT NULL,
    evidence jsonb NOT NULL
);

CREATE TABLE conversion.destinations (
    id bigint NOT NULL,
    environment text NOT NULL,
    media text NOT NULL,
    pixel_id text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    retired_at timestamp with time zone,
    next_claim_at timestamp with time zone DEFAULT '-infinity'::timestamp with time zone NOT NULL,
    credential_id bigint,
    legacy_fb_pixel_id bigint,
    consecutive_payment_claims integer DEFAULT 0 NOT NULL,
    blocked_credential_id bigint,
    nonpayment_claim_streak integer DEFAULT 0 NOT NULL,
    credential_health_batch_id bigint,
    CONSTRAINT destinations_media_check CHECK ((media = 'meta'::text)),
    CONSTRAINT destinations_nonpayment_claim_streak_check CHECK (((nonpayment_claim_streak >= 0) AND (nonpayment_claim_streak <= 4))),
    CONSTRAINT destinations_pixel_id_check CHECK ((pixel_id ~ '^[0-9]+$'::text))
);

ALTER TABLE conversion.destinations ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.destinations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.events (
    id bigint NOT NULL,
    integration_id bigint NOT NULL,
    environment text NOT NULL,
    source_event_type text NOT NULL,
    source_event_id text NOT NULL,
    event_name text NOT NULL,
    source_order_id text,
    promotion_id text,
    attribution_state text NOT NULL,
    reason text,
    occurred_at timestamp with time zone,
    received_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    amount numeric,
    currency character varying(3),
    action_source text,
    source_url text,
    matching_data jsonb DEFAULT '{}'::jsonb NOT NULL,
    partner_fields jsonb,
    order_type text,
    time_source text,
    payment_app_scope text,
    payment_policy_decision_id bigint,
    payment_policy_state text DEFAULT 'historical'::text NOT NULL,
    CONSTRAINT events_attribution_state_check CHECK ((attribution_state = ANY (ARRAY['matched'::text, 'organic'::text, 'missing_pixel'::text, 'unknown'::text]))),
    CONSTRAINT events_check CHECK (((payment_policy_state = 'decided'::text) = (payment_policy_decision_id IS NOT NULL))),
    CONSTRAINT events_check1 CHECK (((payment_policy_state <> ALL (ARRAY['pending'::text, 'decided'::text])) OR ((event_name = ANY (ARRAY['Purchase'::text, 'Subscribe'::text])) AND (payment_app_scope IS NOT NULL) AND (payment_app_scope <> ''::text) AND (source_order_id IS NOT NULL) AND (source_order_id <> ''::text)))),
    CONSTRAINT events_event_name_check CHECK ((event_name = ANY (ARRAY['CompleteRegistration'::text, 'InitiateCheckout'::text, 'Purchase'::text, 'Subscribe'::text]))),
    CONSTRAINT events_payment_policy_state_check CHECK ((payment_policy_state = ANY (ARRAY['historical'::text, 'not_applicable'::text, 'pending'::text, 'decided'::text])))
);

ALTER TABLE conversion.events ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.inbox (
    id bigint NOT NULL,
    integration_id bigint NOT NULL,
    source_event_type text NOT NULL,
    source_event_id text,
    received_at timestamp with time zone DEFAULT now() NOT NULL,
    payload_digest bytea,
    payload jsonb,
    state text DEFAULT 'pending'::text NOT NULL,
    reason text,
    source_order_id text,
    digest_version smallint DEFAULT 1 NOT NULL,
    source_app_id text,
    normalized_event_id bigint,
    processed_at timestamp with time zone,
    CONSTRAINT inbox_check CHECK (((state = 'quarantined'::text) OR ((source_event_id IS NOT NULL) AND (payload_digest IS NOT NULL)))),
    CONSTRAINT inbox_digest_version_check CHECK ((digest_version > 0)),
    CONSTRAINT inbox_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'consumed'::text, 'quarantined'::text])))
);

ALTER TABLE conversion.inbox ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.inbox_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.integration_destinations (
    integration_id bigint NOT NULL,
    destination_id bigint NOT NULL,
    environment text NOT NULL,
    default_route boolean DEFAULT false NOT NULL
);

CREATE TABLE conversion.integrations (
    id bigint NOT NULL,
    source text NOT NULL,
    account_key text NOT NULL,
    environment text NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    allowed_apps text[] DEFAULT '{}'::text[] NOT NULL,
    allowed_events text[] DEFAULT '{}'::text[] NOT NULL,
    contract_version text NOT NULL,
    inbound_credential_id bigint,
    report_credential_id bigint,
    report_config jsonb DEFAULT '{}'::jsonb NOT NULL
);

ALTER TABLE conversion.integrations ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.integrations_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.legacy_outbox_imports (
    legacy_event_id text NOT NULL,
    event_id bigint NOT NULL,
    legacy_state text NOT NULL,
    legacy_attempts integer NOT NULL,
    legacy_delivered_at timestamp with time zone,
    legacy_pixel_id text,
    legacy_next_attempt_at timestamp with time zone NOT NULL,
    legacy_had_lease boolean NOT NULL,
    resolution text NOT NULL,
    environment_evidence text,
    imported_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT legacy_outbox_imports_legacy_attempts_check CHECK ((legacy_attempts >= 0))
);

CREATE TABLE conversion.meta_batches (
    id bigint NOT NULL,
    environment text NOT NULL,
    destination_id bigint NOT NULL,
    lease_token uuid DEFAULT gen_random_uuid() NOT NULL,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    lease_until timestamp with time zone NOT NULL,
    started_at timestamp with time zone,
    sent_count integer,
    credential_id bigint,
    key_version text,
    receipt_at timestamp with time zone,
    receipt jsonb,
    late_receipt_at timestamp with time zone,
    late_receipt jsonb,
    finished_at timestamp with time zone,
    confirmed boolean GENERATED ALWAYS AS (COALESCE((((receipt ->> 'Outcome'::text) = 'accepted'::text) AND ((receipt ->> 'HTTPStatus'::text) = '200'::text) AND ((receipt ->> 'GraphCode'::text) IS NULL) AND ((receipt ->> 'EventsReceived'::text) = (sent_count)::text)), false)) STORED,
    CONSTRAINT meta_batches_check CHECK (((started_at IS NULL) = (sent_count IS NULL))),
    CONSTRAINT meta_batches_check1 CHECK (((started_at IS NULL) OR (sent_count BETWEEN 1 AND 100 AND credential_id IS NOT NULL AND key_version IS NOT NULL))),
    CONSTRAINT meta_batches_check2 CHECK (((receipt_at IS NULL) = (receipt IS NULL))),
    CONSTRAINT meta_batches_check3 CHECK (((late_receipt_at IS NULL) = (late_receipt IS NULL))),
    CONSTRAINT meta_batches_check4 CHECK (((receipt IS NULL) OR ((started_at IS NOT NULL) AND (((receipt ->> 'Outcome'::text) IS DISTINCT FROM 'accepted'::text) OR confirmed))))
);

ALTER TABLE conversion.meta_batches ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.meta_batches_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.operation_audit (
    id bigint NOT NULL,
    environment text NOT NULL,
    action text NOT NULL,
    subject_id text NOT NULL,
    operator_id text NOT NULL,
    operator_role text NOT NULL,
    reason text NOT NULL,
    evidence_reference text,
    before_state jsonb NOT NULL,
    after_state jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT operation_audit_reason_check CHECK (((char_length(reason) >= 1) AND (char_length(reason) <= 500)))
);

ALTER TABLE conversion.operation_audit ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.operation_audit_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.partner_report_attempts (
    report_id bigint NOT NULL,
    attempt_no integer NOT NULL,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    sending_at timestamp with time zone,
    recorded_at timestamp with time zone,
    outcome text,
    reason text,
    retry_at timestamp with time zone,
    replay_allowed boolean DEFAULT false NOT NULL,
    http_status smallint,
    business_code bigint,
    responded_at timestamp with time zone,
    lease_token uuid,
    CONSTRAINT partner_report_attempts_http_status_check CHECK (((http_status >= 100) AND (http_status <= 599)))
);

CREATE TABLE conversion.partner_reports (
    id bigint NOT NULL,
    integration_id bigint NOT NULL,
    event_id bigint NOT NULL,
    original_delivery_id bigint NOT NULL,
    delivery_role text DEFAULT 'original'::text NOT NULL,
    report_status smallint NOT NULL,
    contract_version text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    reason text,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_token uuid,
    lease_until timestamp with time zone,
    wire_payload bytea,
    CONSTRAINT partner_reports_delivery_role_check CHECK ((delivery_role = 'original'::text)),
    CONSTRAINT partner_reports_report_status_check CHECK ((report_status = ANY (ARRAY['-1'::integer, 0, 1]))),
    CONSTRAINT partner_reports_state_check CHECK ((state = ANY (ARRAY['pending'::text, 'blocked'::text, 'outcome_unknown'::text, 'confirmed'::text])))
);

ALTER TABLE conversion.partner_reports ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.partner_reports_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE SEQUENCE conversion.payment_policy_branch_ids
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE conversion.payment_policy_counters (
    environment text NOT NULL,
    integration_id bigint NOT NULL,
    app_scope text NOT NULL,
    scope_kind text NOT NULL,
    promotion_key text NOT NULL,
    branch_id bigint NOT NULL,
    last_sequence bigint NOT NULL,
    CONSTRAINT payment_policy_counters_app_scope_check CHECK ((app_scope <> ''::text)),
    CONSTRAINT payment_policy_counters_branch_id_check CHECK ((branch_id > 0)),
    CONSTRAINT payment_policy_counters_check CHECK ((((scope_kind = 'default'::text) AND (promotion_key = ''::text)) OR ((scope_kind = 'promotion'::text) AND (promotion_key <> ''::text)))),
    CONSTRAINT payment_policy_counters_last_sequence_check CHECK (((last_sequence >= 1) AND (last_sequence <= '9007199254740991'::bigint)))
);

CREATE TABLE conversion.payment_policy_decisions (
    id bigint NOT NULL,
    environment text NOT NULL,
    integration_id bigint NOT NULL,
    app_scope text NOT NULL,
    payment_key text NOT NULL,
    scope_kind text NOT NULL,
    promotion_key text NOT NULL,
    original_destination_id bigint NOT NULL,
    policy_scope_id bigint,
    policy_version_id bigint,
    branch_id bigint,
    mode text,
    cycle_sequence bigint,
    sample_bucket integer,
    sample_version smallint,
    outcome text,
    reason text,
    amount_minor bigint,
    currency text,
    payment_kind text NOT NULL,
    decided_at timestamp with time zone,
    CONSTRAINT payment_policy_decisions_amount_minor_check CHECK ((amount_minor >= 0)),
    CONSTRAINT payment_policy_decisions_app_scope_check CHECK ((app_scope <> ''::text)),
    CONSTRAINT payment_policy_decisions_branch_id_check CHECK ((branch_id > 0)),
    CONSTRAINT payment_policy_decisions_check CHECK ((((scope_kind = 'default'::text) AND (promotion_key = ''::text)) OR ((scope_kind = 'promotion'::text) AND (promotion_key <> ''::text)))),
    CONSTRAINT payment_policy_decisions_check1 CHECK (((policy_scope_id IS NULL) = (policy_version_id IS NULL))),
    CONSTRAINT payment_policy_decisions_check2 CHECK (((branch_id IS NULL) OR (policy_version_id IS NOT NULL))),
    CONSTRAINT payment_policy_decisions_check3 CHECK (((NOT (mode IS DISTINCT FROM 'cycle'::text)) = (cycle_sequence IS NOT NULL))),
    CONSTRAINT payment_policy_decisions_check4 CHECK (((sample_bucket IS NULL) = (sample_version IS NULL))),
    CONSTRAINT payment_policy_decisions_check5 CHECK (((NOT (mode IS DISTINCT FROM 'ratio'::text)) = (sample_bucket IS NOT NULL))),
    CONSTRAINT payment_policy_decisions_check6 CHECK ((((outcome IS NULL) = (decided_at IS NULL)) AND ((outcome IS NULL) = (reason IS NULL)))),
    CONSTRAINT payment_policy_decisions_currency_check CHECK ((currency ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT payment_policy_decisions_cycle_sequence_check CHECK (((cycle_sequence >= 1) AND (cycle_sequence <= '9007199254740991'::bigint))),
    CONSTRAINT payment_policy_decisions_mode_check CHECK ((mode = ANY (ARRAY['all'::text, 'ratio'::text, 'cycle'::text]))),
    CONSTRAINT payment_policy_decisions_outcome_check CHECK ((outcome = ANY (ARRAY['selected'::text, 'skipped'::text]))),
    CONSTRAINT payment_policy_decisions_payment_key_check CHECK ((payment_key <> ''::text)),
    CONSTRAINT payment_policy_decisions_sample_bucket_check CHECK (((sample_bucket >= 0) AND (sample_bucket <= 9999))),
    CONSTRAINT payment_policy_decisions_sample_version_check CHECK ((sample_version > 0))
);

ALTER TABLE conversion.payment_policy_decisions ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.payment_policy_decisions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.payment_policy_scopes (
    id bigint NOT NULL,
    environment text NOT NULL,
    integration_id bigint NOT NULL,
    app_scope text NOT NULL,
    scope_kind text NOT NULL,
    promotion_key text NOT NULL,
    active_version_id bigint,
    CONSTRAINT payment_policy_scopes_app_scope_check CHECK ((app_scope <> ''::text)),
    CONSTRAINT payment_policy_scopes_check CHECK ((((scope_kind = 'default'::text) AND (promotion_key = ''::text)) OR ((scope_kind = 'promotion'::text) AND (promotion_key <> ''::text))))
);

ALTER TABLE conversion.payment_policy_scopes ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.payment_policy_scopes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.payment_policy_versions (
    id bigint NOT NULL,
    scope_id bigint NOT NULL,
    version_no bigint NOT NULL,
    config jsonb NOT NULL,
    published_by text NOT NULL,
    published_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_policy_versions_config_check CHECK ((jsonb_typeof(config) = 'object'::text)),
    CONSTRAINT payment_policy_versions_version_no_check CHECK ((version_no > 0))
);

ALTER TABLE conversion.payment_policy_versions ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME conversion.payment_policy_versions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE conversion.worker_heartbeats (
    environment text NOT NULL,
    worker_type text NOT NULL,
    instance_id text NOT NULL,
    heartbeat_at timestamp with time zone NOT NULL,
    paused boolean NOT NULL,
    last_progress_at timestamp with time zone
);

CREATE TABLE public.admin_audit_logs (
    id bigint NOT NULL,
    admin_id character varying(36),
    action character varying(50) NOT NULL,
    target_id character varying(100),
    before_data jsonb,
    after_data jsonb,
    ip_address character varying(45),
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

CREATE SEQUENCE public.admin_audit_logs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.admin_audit_logs_id_seq OWNED BY public.admin_audit_logs.id;

CREATE TABLE public.admins (
    id character varying(36) NOT NULL,
    username character varying(50) NOT NULL,
    password_hash character varying(255) NOT NULL,
    nickname character varying(50),
    role character varying(64) NOT NULL,
    status smallint DEFAULT 1,
    tfa_secret character varying(100),
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    role_id bigint NOT NULL
);

CREATE TABLE public.bookshelves (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    novel_id integer NOT NULL,
    chapter_index integer DEFAULT 0,
    scroll_offset_percentage numeric(5,4) DEFAULT 0.0000,
    in_shelf boolean DEFAULT true,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    progress_version character varying(64) DEFAULT ''::character varying NOT NULL,
    shelf_added_at timestamp with time zone
);

CREATE SEQUENCE public.bookshelves_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.bookshelves_id_seq OWNED BY public.bookshelves.id;

CREATE TABLE public.chapter_content_objects (
    object_key text NOT NULL,
    chapter_id character varying(64) NOT NULL,
    sha256 text NOT NULL,
    state text DEFAULT 'staged'::text NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT (now() + '1 day'::interval) NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT chapter_content_objects_state_check CHECK ((state = ANY (ARRAY['staged'::text, 'published'::text, 'retired'::text, 'deleting'::text, 'deleted'::text])))
);

CREATE TABLE public.chapter_entitlement_links (
    chapter_id character varying(64) NOT NULL,
    source_chapter_id character varying(64) NOT NULL,
    novel_id integer NOT NULL,
    purchased_before timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT chapter_entitlement_links_check CHECK (((source_chapter_id)::text <> (chapter_id)::text))
);

CREATE TABLE public.chapters (
    id character varying(64) NOT NULL,
    novel_id integer NOT NULL,
    chapter_index integer,
    title character varying(150) NOT NULL,
    content text NOT NULL,
    word_count integer DEFAULT 0,
    is_paid boolean DEFAULT false,
    price integer DEFAULT 50,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    content_sha256 text,
    content_object_key text,
    content_preview text,
    body_revision bigint DEFAULT 0 NOT NULL
);

CREATE TABLE public.fb_pixels (
    id integer NOT NULL,
    name character varying(255) NOT NULL,
    pixel_id character varying(100) NOT NULL,
    access_token text NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

CREATE SEQUENCE public.fb_pixels_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.fb_pixels_id_seq OWNED BY public.fb_pixels.id;

CREATE TABLE public.feedback (
    id bigint NOT NULL,
    user_id bigint,
    email character varying(100),
    subject character varying(150),
    content text NOT NULL,
    status character varying(20) DEFAULT 'pending'::character varying,
    admin_reply text,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

CREATE SEQUENCE public.feedback_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.feedback_id_seq OWNED BY public.feedback.id;

CREATE TABLE public.gift_code_redemptions (
    id integer NOT NULL,
    gift_code_id integer NOT NULL,
    user_id bigint NOT NULL,
    redeemed_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

CREATE SEQUENCE public.gift_code_redemptions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.gift_code_redemptions_id_seq OWNED BY public.gift_code_redemptions.id;

CREATE TABLE public.gift_codes (
    id integer NOT NULL,
    code character varying(50) NOT NULL,
    reward_type character varying(20) NOT NULL,
    reward_amount integer NOT NULL,
    max_redemptions integer DEFAULT 1000,
    current_redemptions integer DEFAULT 0,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

CREATE SEQUENCE public.gift_codes_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.gift_codes_id_seq OWNED BY public.gift_codes.id;

CREATE TABLE public.legacy_whole_book_proofs (
    order_id bigint NOT NULL,
    novel_id integer NOT NULL,
    evidence_reference text NOT NULL,
    verified_by character varying(36) NOT NULL,
    verified_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT legacy_whole_book_proofs_evidence_reference_check CHECK ((length(btrim(evidence_reference)) > 0))
);

CREATE SEQUENCE public.novels_id_seq
    START WITH 10000001
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE public.novels (
    id integer DEFAULT nextval('public.novels_id_seq'::regclass) NOT NULL,
    title character varying(150) NOT NULL,
    author character varying(100) NOT NULL,
    cover_url character varying(255),
    rating numeric(2,1) DEFAULT 0.0,
    status character varying(20) DEFAULT 'Ongoing'::character varying,
    publish_status character varying(20) DEFAULT 'published'::character varying NOT NULL,
    synopsis text,
    genres character varying(2048)[] DEFAULT '{}'::character varying[],
    word_count integer DEFAULT 0,
    view_count bigint DEFAULT 0,
    coin_cost_per_thousand integer DEFAULT 500,
    start_pay_chapter_index integer DEFAULT 3,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    content_version bigint DEFAULT 1 NOT NULL
);

CREATE TABLE public.payment_aftercare_policies (
    account_id bigint NOT NULL,
    dispute_lookup boolean DEFAULT false NOT NULL,
    hold_fulfillment boolean DEFAULT false NOT NULL,
    reviewer_id text,
    finance_id text,
    review_seconds integer,
    refund_seconds integer,
    reminder_seconds integer,
    auto_refund boolean DEFAULT false NOT NULL,
    auto_cancel_subscription boolean DEFAULT false NOT NULL,
    dispute_freeze boolean DEFAULT false NOT NULL,
    CONSTRAINT payment_aftercare_policies_check CHECK (((NOT hold_fulfillment) OR ((reviewer_id IS NOT NULL) AND (finance_id IS NOT NULL) AND (review_seconds IS NOT NULL) AND (refund_seconds IS NOT NULL)))),
    CONSTRAINT payment_aftercare_policies_refund_seconds_check CHECK ((refund_seconds > 0)),
    CONSTRAINT payment_aftercare_policies_reminder_seconds_check CHECK ((reminder_seconds > 0)),
    CONSTRAINT payment_aftercare_policies_review_seconds_check CHECK ((review_seconds > 0))
);

CREATE TABLE public.payment_bank_statement_entries (
    id bigint NOT NULL,
    environment text NOT NULL,
    bank_account text NOT NULL,
    transaction_id text NOT NULL,
    statement_ref text NOT NULL,
    booked_at timestamp with time zone NOT NULL,
    currency character varying(3) NOT NULL,
    amount_minor bigint NOT NULL,
    bank_reference text NOT NULL,
    operator text NOT NULL,
    reason text NOT NULL,
    imported_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_bank_statement_entries_amount_minor_check CHECK ((amount_minor <> 0)),
    CONSTRAINT payment_bank_statement_entries_bank_account_check CHECK ((btrim(bank_account) <> ''::text)),
    CONSTRAINT payment_bank_statement_entries_currency_check CHECK (((currency)::text = 'USD'::text)),
    CONSTRAINT payment_bank_statement_entries_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text]))),
    CONSTRAINT payment_bank_statement_entries_operator_check CHECK ((btrim(operator) <> ''::text)),
    CONSTRAINT payment_bank_statement_entries_reason_check CHECK ((btrim(reason) <> ''::text)),
    CONSTRAINT payment_bank_statement_entries_statement_ref_check CHECK ((btrim(statement_ref) <> ''::text)),
    CONSTRAINT payment_bank_statement_entries_transaction_id_check CHECK ((btrim(transaction_id) <> ''::text))
);

ALTER TABLE public.payment_bank_statement_entries ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_bank_statement_entries_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_case_audit (
    case_id bigint NOT NULL,
    request_id uuid NOT NULL,
    actor_id text,
    action text NOT NULL,
    reason text NOT NULL,
    parameters jsonb DEFAULT '{}'::jsonb NOT NULL,
    recorded_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_case_audit_reason_check CHECK ((btrim(reason) <> ''::text))
);

CREATE TABLE public.payment_case_evidence (
    case_id bigint NOT NULL,
    source text NOT NULL,
    source_ref text NOT NULL,
    digest bytea NOT NULL,
    observed_at timestamp with time zone NOT NULL,
    evidence jsonb NOT NULL,
    CONSTRAINT payment_case_evidence_digest_check CHECK ((octet_length(digest) = 32)),
    CONSTRAINT payment_case_evidence_source_check CHECK ((source = ANY (ARRAY['webhook'::text, 'provider_api'::text, 'reconciliation'::text]))),
    CONSTRAINT payment_case_evidence_source_ref_check CHECK ((btrim(source_ref) <> ''::text))
);

CREATE TABLE public.payment_cases (
    id bigint NOT NULL,
    account_id bigint NOT NULL,
    kind text NOT NULL,
    external_id text NOT NULL,
    receipt_refs text[] DEFAULT '{}'::text[] NOT NULL,
    order_ref text,
    provider_status text NOT NULL,
    stage text DEFAULT ''::text NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    outcome text DEFAULT ''::text NOT NULL,
    is_open boolean NOT NULL,
    due_at timestamp with time zone,
    amount_minor bigint,
    sales_mode text,
    observed_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    owner_id text,
    work_state text DEFAULT 'observed'::text NOT NULL,
    work_due_at timestamp with time zone,
    escalated_at timestamp with time zone,
    original_currency text,
    original_amount_minor bigint,
    CONSTRAINT payment_cases_amount_minor_check CHECK ((amount_minor >= 0)),
    CONSTRAINT payment_cases_external_id_check CHECK ((btrim(external_id) <> ''::text)),
    CONSTRAINT payment_cases_kind_check CHECK ((kind = ANY (ARRAY['risk_signal'::text, 'risk_review'::text, 'dispute'::text, 'reversal'::text]))),
    CONSTRAINT payment_cases_original_amount_minor_check CHECK (((original_amount_minor IS NULL) OR (original_amount_minor >= 0))),
    CONSTRAINT payment_cases_original_currency_check CHECK (((original_currency IS NULL) OR (original_currency ~ '^[A-Z]{3}$'::text))),
    CONSTRAINT payment_cases_sales_mode_check CHECK ((sales_mode = ANY (ARRAY['direct'::text, 'mor'::text]))),
    CONSTRAINT payment_cases_work_state_check CHECK ((work_state = ANY (ARRAY['observed'::text, 'pending'::text, 'approved'::text, 'refund_due'::text, 'dispute_due'::text, 'closed'::text])))
);

CREATE TABLE public.payment_channel_accounts (
    id bigint NOT NULL,
    provider text NOT NULL,
    environment text NOT NULL,
    account_key text NOT NULL,
    credentials_ref text NOT NULL,
    webhook_ref text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    new_orders_enabled boolean DEFAULT false NOT NULL,
    new_subscriptions_enabled boolean DEFAULT false NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    merchant_id text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    is_primary boolean DEFAULT false NOT NULL,
    active_version integer DEFAULT 1 NOT NULL,
    pending_version integer DEFAULT 1 NOT NULL,
    webhook_token uuid DEFAULT gen_random_uuid() NOT NULL,
    last_validated_at timestamp with time zone,
    validation_summary jsonb,
    published_at timestamp with time zone,
    published_by character varying(36),
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    adaptive_pricing_enabled boolean DEFAULT false NOT NULL,
    saved_payment_method_enabled boolean DEFAULT false NOT NULL,
    simplified_address_collection_enabled boolean DEFAULT false NOT NULL,
    CONSTRAINT chk_channel_primary_published CHECK (((NOT is_primary) OR (status = 'published'::text))),
    CONSTRAINT chk_channel_status CHECK ((status = ANY (ARRAY['draft'::text, 'verified'::text, 'published'::text, 'disabled'::text]))),
    CONSTRAINT chk_channel_switches_published CHECK ((((NOT new_orders_enabled) AND (NOT new_subscriptions_enabled)) OR (status = 'published'::text))),
    CONSTRAINT payment_channel_accounts_account_key_check CHECK ((btrim(account_key) <> ''::text)),
    CONSTRAINT payment_channel_accounts_credentials_ref_check CHECK ((btrim(credentials_ref) <> ''::text)),
    CONSTRAINT payment_channel_accounts_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text]))),
    CONSTRAINT payment_channel_accounts_provider_check CHECK ((provider = ANY (ARRAY['paypal'::text, 'stripe'::text]))),
    CONSTRAINT payment_channel_accounts_webhook_ref_check CHECK ((btrim(webhook_ref) <> ''::text))
);

CREATE TABLE public.third_party_payment_orders (
    id bigint NOT NULL,
    order_id bigint NOT NULL,
    payment_provider character varying(30) NOT NULL,
    external_order_id character varying(100),
    capture_id character varying(100),
    payer_id character varying(100),
    payer_email character varying(255),
    payer_name character varying(150),
    payer_country character varying(10),
    currency character varying(10) DEFAULT 'USD'::character varying,
    gross_amount numeric(10,2) NOT NULL,
    fee_amount numeric(10,2),
    net_amount numeric(10,2),
    status character varying(30) NOT NULL,
    seller_protection_status character varying(50),
    raw_payload jsonb,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    account_id bigint,
    object_type text,
    reconciliation_note text,
    presentment_currency text,
    presentment_amount_minor bigint,
    CONSTRAINT payment_receipt_scoped_identity CHECK (((account_id IS NULL) OR ((object_type IS NOT NULL) AND (capture_id IS NOT NULL) AND ((capture_id)::text <> ''::text))))
);

CREATE VIEW public.payment_case_receipts AS
 SELECT c.id AS case_id,
    t.id AS receipt_id,
    t.order_id,
    t.capture_id
   FROM ((public.payment_cases c
     JOIN public.payment_channel_accounts a ON ((a.id = c.account_id)))
     JOIN public.third_party_payment_orders t ON (((t.account_id = c.account_id) AND ((t.payment_provider)::text = a.provider) AND (c.receipt_refs @> ARRAY[(t.capture_id)::text]) AND ((t.status)::text = 'VERIFIED'::text) AND ((t.currency)::text = 'USD'::text))))
  WHERE (NOT (EXISTS ( SELECT 1
           FROM public.third_party_payment_orders duplicate
          WHERE ((duplicate.account_id = t.account_id) AND ((duplicate.payment_provider)::text = (t.payment_provider)::text) AND ((duplicate.capture_id)::text = (t.capture_id)::text) AND ((duplicate.status)::text = 'VERIFIED'::text) AND (duplicate.id <> t.id)))));

CREATE TABLE public.payment_case_reminders (
    case_id bigint NOT NULL,
    due_at timestamp with time zone NOT NULL,
    recorded_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL
);

ALTER TABLE public.payment_cases ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_cases_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_channel_account_versions (
    account_id bigint NOT NULL,
    version integer NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    merchant_id text DEFAULT ''::text NOT NULL,
    account_key text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    validation_status text DEFAULT 'pending'::text NOT NULL,
    validation_summary jsonb,
    validated_at timestamp with time zone,
    created_by character varying(36),
    published_by character varying(36),
    published_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_channel_account_versions_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'verified'::text, 'published'::text, 'superseded'::text]))),
    CONSTRAINT payment_channel_account_versions_validation_status_check CHECK ((validation_status = ANY (ARRAY['pending'::text, 'verified'::text, 'failed'::text]))),
    CONSTRAINT payment_channel_account_versions_version_check CHECK ((version > 0))
);

ALTER TABLE public.payment_channel_accounts ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_channel_accounts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_channel_credentials (
    id bigint NOT NULL,
    account_id bigint NOT NULL,
    version integer NOT NULL,
    purpose text NOT NULL,
    key_version text NOT NULL,
    ciphertext text NOT NULL,
    hint text DEFAULT ''::text NOT NULL,
    expires_at timestamp with time zone,
    created_by character varying(36),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_channel_credentials_purpose_check CHECK ((purpose = ANY (ARRAY['client_id'::text, 'api_secret'::text, 'webhook_id'::text, 'previous_webhook_id'::text, 'api_key'::text, 'webhook_secret'::text, 'previous_webhook_secret'::text])))
);

CREATE SEQUENCE public.payment_channel_credentials_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.payment_channel_credentials_id_seq OWNED BY public.payment_channel_credentials.id;

CREATE TABLE public.payment_checkout_attempts (
    id uuid NOT NULL,
    order_id bigint NOT NULL,
    provider text NOT NULL,
    account_id bigint NOT NULL,
    sales_mode text NOT NULL,
    status text NOT NULL,
    provider_order_id text,
    approval_url text,
    return_url text NOT NULL,
    cancel_url text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    first_sent_at timestamp with time zone,
    retry_until timestamp with time zone,
    create_lookup_only boolean DEFAULT false NOT NULL,
    next_create_at timestamp with time zone DEFAULT now() NOT NULL,
    capture_first_sent_at timestamp with time zone,
    capture_retry_until timestamp with time zone,
    capture_lookup_only boolean DEFAULT false NOT NULL,
    next_capture_at timestamp with time zone,
    capture_outcome text,
    paypal_switch_token uuid,
    CONSTRAINT payment_checkout_attempts_capture_outcome_check CHECK ((capture_outcome = ANY (ARRAY['payment_blocked'::text, 'payment_declined'::text, 'payment_action_required'::text, 'payment_unavailable'::text, 'payment_unconfirmed'::text]))),
    CONSTRAINT payment_checkout_attempts_sales_mode_check CHECK ((sales_mode = ANY (ARRAY['direct'::text, 'merchant_of_record'::text]))),
    CONSTRAINT payment_checkout_attempts_status_check CHECK ((status = ANY (ARRAY['creating'::text, 'buyer_action'::text, 'capturing'::text, 'settled'::text, 'closed'::text])))
);

CREATE TABLE public.payment_checkout_decisions (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    checkout_environment text NOT NULL,
    stage text NOT NULL,
    provider text,
    account_id bigint,
    order_id bigint,
    checkout_id uuid,
    slot_id integer,
    outcome text NOT NULL,
    reason text NOT NULL,
    rule_version integer NOT NULL,
    evaluated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT payment_checkout_decisions_check CHECK (((account_id IS NULL) OR (provider IS NOT NULL))),
    CONSTRAINT payment_checkout_decisions_checkout_environment_check CHECK ((btrim(checkout_environment) <> ''::text)),
    CONSTRAINT payment_checkout_decisions_outcome_check CHECK ((outcome = ANY (ARRAY['allow'::text, 'block'::text, 'verify'::text, 'review'::text]))),
    CONSTRAINT payment_checkout_decisions_provider_check CHECK ((provider = ANY (ARRAY['paypal'::text, 'stripe'::text]))),
    CONSTRAINT payment_checkout_decisions_reason_check CHECK ((btrim(reason) <> ''::text)),
    CONSTRAINT payment_checkout_decisions_rule_version_check CHECK ((rule_version > 0)),
    CONSTRAINT payment_checkout_decisions_slot_id_check CHECK ((slot_id > 0)),
    CONSTRAINT payment_checkout_decisions_stage_check CHECK ((stage = ANY (ARRAY['purchase_intent'::text, 'payment_attempt'::text, 'subscription_intent'::text])))
);

ALTER TABLE public.payment_checkout_decisions ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_checkout_decisions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_checkout_requests (
    environment text NOT NULL,
    user_id bigint NOT NULL,
    request_key uuid NOT NULL,
    slot_id integer NOT NULL,
    novel_id bigint NOT NULL,
    return_path text NOT NULL,
    order_id bigint,
    product_id bigint,
    vip_intent_id uuid,
    CONSTRAINT chk_checkout_request_target CHECK (((order_id IS NOT NULL) <> (vip_intent_id IS NOT NULL)))
);

CREATE TABLE public.payment_deferred_fulfillment (
    order_id bigint NOT NULL,
    receipt_id bigint NOT NULL,
    case_id bigint,
    deferred_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    next_check_at timestamp with time zone DEFAULT now() NOT NULL,
    resolved_at timestamp with time zone
);

CREATE TABLE public.payment_entitlement_adjustments (
    order_id bigint NOT NULL,
    blocked boolean NOT NULL,
    charged_coins integer NOT NULL,
    bonus_coins integer NOT NULL,
    policy_evidence text NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    charged_coin_shortfall integer DEFAULT 0 NOT NULL,
    bonus_coin_shortfall integer DEFAULT 0 NOT NULL,
    freeze_status text DEFAULT 'none'::text NOT NULL,
    freeze_case_id bigint,
    CONSTRAINT payment_entitlement_adjustments_bonus_coin_shortfall_check CHECK ((bonus_coin_shortfall >= 0)),
    CONSTRAINT payment_entitlement_adjustments_bonus_coins_check CHECK ((bonus_coins >= 0)),
    CONSTRAINT payment_entitlement_adjustments_charged_coin_shortfall_check CHECK ((charged_coin_shortfall >= 0)),
    CONSTRAINT payment_entitlement_adjustments_charged_coins_check CHECK ((charged_coins >= 0)),
    CONSTRAINT payment_entitlement_adjustments_freeze_status_check CHECK ((freeze_status = ANY (ARRAY['none'::text, 'frozen'::text, 'written_off'::text])))
);

CREATE TABLE public.payment_event_inbox (
    event_id text NOT NULL,
    event_type text NOT NULL,
    capture_id text,
    refund_id text,
    amount_cents bigint NOT NULL,
    currency text NOT NULL,
    event jsonb NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    id bigint NOT NULL,
    provider text NOT NULL,
    account_id bigint,
    CONSTRAINT payment_event_account_required CHECK (((provider = 'paypal'::text) OR (account_id IS NOT NULL))),
    CONSTRAINT payment_event_inbox_provider_check CHECK ((provider = ANY (ARRAY['paypal'::text, 'stripe'::text])))
);

ALTER TABLE public.payment_event_inbox ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_event_inbox_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_event_outbox (
    event_id text NOT NULL,
    order_id bigint NOT NULL,
    event_name text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    state text DEFAULT 'pending'::text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    lease_token uuid,
    lease_until timestamp with time zone,
    delivered_at timestamp with time zone,
    last_error text
);

CREATE TABLE public.payment_event_recovery (
    account_id bigint NOT NULL,
    window_start timestamp with time zone NOT NULL,
    window_end timestamp with time zone NOT NULL,
    page_cursor text DEFAULT ''::text NOT NULL,
    last_complete_at timestamp with time zone,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_token uuid,
    lease_until timestamp with time zone,
    last_error text,
    CONSTRAINT payment_event_recovery_check CHECK ((window_end > window_start))
);

CREATE TABLE public.payment_event_recovery_failures (
    account_id bigint NOT NULL,
    event_id text NOT NULL,
    event jsonb NOT NULL,
    last_error text NOT NULL,
    failed_at timestamp with time zone DEFAULT now() NOT NULL,
    next_attempt_at timestamp with time zone,
    lease_token uuid,
    lease_until timestamp with time zone,
    resolved_at timestamp with time zone
);

CREATE TABLE public.payment_event_recovery_groups (
    account_id bigint NOT NULL,
    group_key text NOT NULL,
    window_start timestamp with time zone NOT NULL,
    window_end timestamp with time zone NOT NULL,
    page_cursor text DEFAULT ''::text NOT NULL,
    last_complete_at timestamp with time zone,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_token uuid,
    lease_until timestamp with time zone,
    last_error text,
    CONSTRAINT payment_event_recovery_groups_check CHECK ((window_end > window_start)),
    CONSTRAINT payment_event_recovery_groups_group_key_check CHECK ((btrim(group_key) <> ''::text))
);

CREATE TABLE public.payment_fulfillment_funding (
    order_id bigint NOT NULL,
    receipt_id bigint NOT NULL,
    principal_minor bigint,
    currency character varying(3) NOT NULL,
    CONSTRAINT payment_fulfillment_funding_principal_minor_check CHECK ((principal_minor >= 0))
);

CREATE TABLE public.payment_ledger_amounts (
    movement_id bigint NOT NULL,
    component text NOT NULL,
    currency character varying(3) NOT NULL,
    amount_minor bigint NOT NULL,
    evidence_id bigint NOT NULL,
    CONSTRAINT payment_ledger_amounts_component_check CHECK ((component = ANY (ARRAY['gross'::text, 'fee'::text, 'net'::text, 'sales_tax'::text, 'withheld_tax'::text, 'processing_fee'::text, 'customer_facing'::text, 'fx_fee'::text]))),
    CONSTRAINT payment_ledger_amounts_currency_check CHECK (((currency)::text ~ '^[A-Z]{3}$'::text))
);

CREATE TABLE public.payment_ledger_evidence (
    id bigint NOT NULL,
    movement_id bigint NOT NULL,
    source text NOT NULL,
    source_ref text NOT NULL,
    payload_hash bytea NOT NULL,
    payload jsonb NOT NULL,
    conflicts boolean DEFAULT false NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_ledger_evidence_payload_hash_check CHECK ((octet_length(payload_hash) = 32)),
    CONSTRAINT payment_ledger_evidence_source_check CHECK ((source = ANY (ARRAY['paypal_transaction_search'::text, 'paypal_settlement'::text, 'stripe_balance_transaction'::text, 'stripe_report'::text, 'stripe_webhook'::text, 'stripe_refund_lookup'::text]))),
    CONSTRAINT payment_ledger_evidence_source_ref_check CHECK ((btrim(source_ref) <> ''::text))
);

ALTER TABLE public.payment_ledger_evidence ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_ledger_evidence_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_ledger_movements (
    id bigint NOT NULL,
    account_id bigint NOT NULL,
    movement_key text NOT NULL,
    kind text,
    provider_code text,
    currency character varying(3),
    exchange_rate numeric,
    occurred_at timestamp with time zone,
    posted_at timestamp with time zone,
    review_required boolean DEFAULT false NOT NULL,
    CONSTRAINT payment_ledger_movements_check CHECK ((((posted_at IS NULL) = (kind IS NULL)) AND ((posted_at IS NULL) = (currency IS NULL)))),
    CONSTRAINT payment_ledger_movements_currency_check CHECK (((currency)::text ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT payment_ledger_movements_exchange_rate_check CHECK (((exchange_rate > (0)::numeric) AND (exchange_rate < 'Infinity'::numeric))),
    CONSTRAINT payment_ledger_movements_kind_check CHECK ((kind = ANY (ARRAY['payment'::text, 'refund'::text, 'reversal'::text, 'dispute_debit'::text, 'dispute_credit'::text, 'fee'::text, 'tax'::text, 'fx'::text, 'payout'::text, 'hold'::text, 'release'::text, 'other'::text]))),
    CONSTRAINT payment_ledger_movements_movement_key_check CHECK ((btrim(movement_key) <> ''::text))
);

ALTER TABLE public.payment_ledger_movements ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_ledger_movements_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_ledger_references (
    movement_id bigint NOT NULL,
    object_type text NOT NULL,
    object_id text NOT NULL,
    CONSTRAINT payment_ledger_references_object_id_check CHECK ((btrim(object_id) <> ''::text)),
    CONSTRAINT payment_ledger_references_object_type_check CHECK ((btrim(object_type) <> ''::text))
);

CREATE VIEW public.payment_ledger_receipt_matches AS
 SELECT m.id AS movement_id,
    p.id AS receipt_id
   FROM ((public.payment_ledger_movements m
     JOIN public.payment_ledger_references r ON ((r.movement_id = m.id)))
     JOIN public.third_party_payment_orders p ON (((p.account_id = m.account_id) AND ((p.capture_id)::text = r.object_id) AND ((p.status)::text = 'VERIFIED'::text) AND (((r.object_type = 'stripe_charge'::text) AND ((p.payment_provider)::text = 'stripe'::text) AND (p.object_type = 'charge'::text)) OR ((r.object_type = 'paypal_transaction'::text) AND ((p.payment_provider)::text = 'paypal'::text) AND (p.object_type = ANY (ARRAY['capture'::text, 'sale'::text])))))))
  WHERE ((m.posted_at IS NOT NULL) AND (NOT m.review_required) AND (NOT (EXISTS ( SELECT 1
           FROM public.payment_ledger_references other
          WHERE ((other.movement_id = m.id) AND (other.object_type = r.object_type) AND (other.object_id <> r.object_id))))) AND (NOT (EXISTS ( SELECT 1
           FROM public.third_party_payment_orders other
          WHERE ((other.account_id = p.account_id) AND ((other.payment_provider)::text = (p.payment_provider)::text) AND ((other.capture_id)::text = (p.capture_id)::text) AND (other.id <> p.id) AND ((other.status)::text = 'VERIFIED'::text) AND (((r.object_type = 'stripe_charge'::text) AND (other.object_type = 'charge'::text)) OR ((r.object_type = 'paypal_transaction'::text) AND (other.object_type = ANY (ARRAY['capture'::text, 'sale'::text])))))))));

CREATE TABLE public.payment_ledger_sync_settings (
    account_id bigint NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    actor_id text NOT NULL,
    reason text NOT NULL,
    window_start timestamp with time zone NOT NULL,
    window_end timestamp with time zone NOT NULL,
    page_cursor text DEFAULT ''::text NOT NULL,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    last_complete_at timestamp with time zone,
    last_error text,
    lease_token uuid,
    lease_until timestamp with time zone,
    CONSTRAINT payment_ledger_sync_settings_check CHECK ((window_end > window_start))
);

CREATE TABLE public.payment_loss_commands (
    id uuid NOT NULL,
    case_id bigint NOT NULL,
    order_id bigint NOT NULL,
    requested_by text NOT NULL,
    blocked boolean NOT NULL,
    evidence_key text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    requested_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    finished_at timestamp with time zone,
    CONSTRAINT payment_loss_commands_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'applied'::text, 'stale'::text])))
);

CREATE TABLE public.payment_paypal_vip_plans (
    slot_id integer NOT NULL,
    provider text DEFAULT 'paypal'::text NOT NULL,
    account_id bigint NOT NULL,
    request_key uuid DEFAULT gen_random_uuid() NOT NULL,
    plan_id text,
    first_sent_at timestamp with time zone,
    review_required boolean DEFAULT false NOT NULL,
    CONSTRAINT payment_paypal_vip_plans_check CHECK (((plan_id IS NULL) OR (NOT review_required))),
    CONSTRAINT payment_paypal_vip_plans_plan_id_check CHECK ((btrim(plan_id) <> ''::text)),
    CONSTRAINT payment_paypal_vip_plans_provider_check CHECK ((provider = 'paypal'::text))
);

CREATE TABLE public.payment_stripe_refund_items (
    account_id bigint NOT NULL,
    refund_ref text NOT NULL,
    receipt_id bigint NOT NULL,
    amount_minor bigint NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_stripe_refund_items_amount_minor_check CHECK ((amount_minor > 0))
);

CREATE TABLE public.paypal_refund_items (
    refund_id text NOT NULL,
    capture_id text NOT NULL,
    currency character varying(3) NOT NULL,
    amount_cents bigint NOT NULL,
    recorded_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT paypal_refund_items_amount_cents_check CHECK ((amount_cents > 0)),
    CONSTRAINT paypal_refund_items_capture_id_check CHECK ((capture_id <> ''::text))
);

CREATE VIEW public.payment_principal_loss_by_receipt AS
 WITH movements AS (
         SELECT x.receipt_id,
            m.id,
            m.kind,
            m.provider_code,
            a.provider,
            v.amount_minor,
            ( SELECT
                        CASE
                            WHEN (count(*) = 1) THEN min(c.outcome)
                            ELSE NULL::text
                        END AS "case"
                   FROM public.payment_cases c
                  WHERE ((c.account_id = m.account_id) AND (c.kind = 'dispute'::text) AND (NOT c.is_open) AND (((a.provider = 'stripe'::text) AND (c.outcome = ANY (ARRAY['lost'::text, 'won'::text])) AND (EXISTS ( SELECT 1
                           FROM public.payment_ledger_references r
                          WHERE ((r.movement_id = m.id) AND (r.object_type = 'stripe_dispute'::text) AND (r.object_id = c.external_id))))) OR ((a.provider = 'paypal'::text) AND (c.outcome = ANY (ARRAY['RESOLVED_BUYER_FAVOUR'::text, 'RESOLVED_SELLER_FAVOUR'::text, 'ACCEPTED'::text])) AND (c.stage = ANY (ARRAY['CHARGEBACK'::text, 'PRE_ARBITRATION'::text, 'ARBITRATION'::text])) AND (EXISTS ( SELECT 1
                           FROM public.payment_case_receipts cr
                          WHERE ((cr.case_id = c.id) AND (cr.receipt_id = x.receipt_id)))) AND (( SELECT count(*) AS count
                           FROM (public.payment_case_receipts other
                             JOIN public.payment_cases d ON ((d.id = other.case_id)))
                          WHERE ((other.receipt_id = x.receipt_id) AND (d.kind = 'dispute'::text))) = 1))))) AS verdict
           FROM (((public.payment_ledger_receipt_matches x
             JOIN public.payment_ledger_movements m ON ((m.id = x.movement_id)))
             JOIN public.payment_channel_accounts a ON ((a.id = m.account_id)))
             JOIN public.payment_ledger_amounts v ON (((v.movement_id = m.id) AND (v.component = 'gross'::text) AND ((v.currency)::text = 'USD'::text))))
          WHERE (((m.currency)::text = 'USD'::text) AND (m.kind = ANY (ARRAY['dispute_debit'::text, 'dispute_credit'::text, 'reversal'::text])) AND (NOT (EXISTS ( SELECT 1
                   FROM (public.payment_ledger_references r
                     JOIN public.third_party_payment_orders t ON ((t.id = x.receipt_id)))
                  WHERE ((r.movement_id = m.id) AND (((r.object_type = 'paypal_refund'::text) AND (EXISTS ( SELECT 1
                           FROM public.paypal_refund_items f
                          WHERE ((f.refund_id = r.object_id) AND (f.capture_id = (t.capture_id)::text) AND ((f.currency)::text = (t.currency)::text))))) OR ((r.object_type = 'stripe_refund'::text) AND (EXISTS ( SELECT 1
                           FROM public.payment_stripe_refund_items f
                          WHERE ((f.refund_ref = r.object_id) AND (f.receipt_id = t.id)))))))))))
        ), eligible AS (
         SELECT movements.receipt_id,
            movements.id,
            movements.kind,
            movements.provider_code,
            movements.provider,
            movements.amount_minor,
            movements.verdict,
            ((movements.provider = 'paypal'::text) AND (movements.kind = 'reversal'::text) AND (movements.provider_code = 'T1106'::text)) AS independent
           FROM movements
          WHERE (((movements.verdict IS NOT NULL) AND (movements.kind = ANY (ARRAY['dispute_debit'::text, 'dispute_credit'::text]))) OR ((movements.provider = 'paypal'::text) AND (movements.kind = 'reversal'::text) AND (movements.provider_code = 'T1106'::text)))
        )
 SELECT receipt_id,
    GREATEST((0)::numeric, (- sum(amount_minor))) AS loss_minor,
    GREATEST((0)::numeric, (- sum(amount_minor) FILTER (WHERE (independent OR (verdict = ANY (ARRAY['lost'::text, 'RESOLVED_BUYER_FAVOUR'::text, 'ACCEPTED'::text])))))) AS confirmed_loss_minor,
    md5(string_agg((((((id)::text || ':'::text) || (amount_minor)::text) || ':'::text) || COALESCE(verdict, ''::text)), ','::text ORDER BY id)) AS evidence_key
   FROM eligible
  GROUP BY receipt_id;

CREATE VIEW public.payment_principal_loss AS
 WITH funding AS (
         SELECT f.order_id,
            f.receipt_id,
            f.principal_minor,
            principal.evidence_key,
            COALESCE(adj.blocked, false) AS blocked,
                CASE
                    WHEN ((t.payment_provider)::text = 'stripe'::text) THEN GREATEST((0)::numeric, ((t.gross_amount * (100)::numeric) - (f.principal_minor)::numeric))
                    ELSE (0)::numeric
                END AS nonprincipal,
            (COALESCE(principal.loss_minor, (0)::numeric) + COALESCE(refund.amount_minor, (0)::numeric)) AS loss_minor,
            (COALESCE(principal.confirmed_loss_minor, (0)::numeric) + COALESCE(refund.amount_minor, (0)::numeric)) AS confirmed_minor,
            COALESCE(refund.evidence, ''::text) AS refund_evidence
           FROM ((((public.payment_fulfillment_funding f
             JOIN public.third_party_payment_orders t ON ((t.id = f.receipt_id)))
             LEFT JOIN LATERAL ( SELECT sum(items.amount_minor) AS amount_minor,
                    string_agg(((items.ref || ':'::text) || items.amount_minor), ','::text ORDER BY items.ref) AS evidence
                   FROM ( SELECT paypal_refund_items.refund_id AS ref,
                            paypal_refund_items.amount_cents AS amount_minor
                           FROM public.paypal_refund_items
                          WHERE (((t.payment_provider)::text = 'paypal'::text) AND (paypal_refund_items.capture_id = (t.capture_id)::text) AND ((paypal_refund_items.currency)::text = (t.currency)::text))
                        UNION ALL
                         SELECT payment_stripe_refund_items.refund_ref,
                            payment_stripe_refund_items.amount_minor
                           FROM public.payment_stripe_refund_items
                          WHERE (((t.payment_provider)::text = 'stripe'::text) AND (payment_stripe_refund_items.receipt_id = t.id))) items) refund ON (true))
             LEFT JOIN public.payment_principal_loss_by_receipt principal ON ((f.receipt_id = principal.receipt_id)))
             LEFT JOIN public.payment_entitlement_adjustments adj ON ((adj.order_id = f.order_id)))
          WHERE ((f.currency)::text = 'USD'::text)
        )
 SELECT order_id,
    sum(
        CASE
            WHEN (principal_minor IS NOT NULL) THEN LEAST((principal_minor)::numeric, GREATEST((0)::numeric, (loss_minor - nonprincipal)))
            ELSE NULL::numeric
        END) AS loss_minor,
    md5(string_agg((((((((receipt_id)::text || ':'::text) || COALESCE((principal_minor)::text, 'unknown'::text)) || ':'::text) || COALESCE(evidence_key, ''::text)) || ':'::text) || refund_evidence), ','::text ORDER BY receipt_id)) AS evidence_key,
        CASE
            WHEN bool_and((principal_minor IS NOT NULL)) THEN (sum(LEAST((principal_minor)::numeric, GREATEST((0)::numeric, (
            CASE
                WHEN blocked THEN loss_minor
                ELSE confirmed_minor
            END - nonprincipal)))) >= sum(principal_minor))
            ELSE bool_or(blocked)
        END AS target_blocked
   FROM funding
  GROUP BY order_id
 HAVING bool_or((evidence_key IS NOT NULL));

CREATE TABLE public.payment_provider_plans (
    id integer NOT NULL,
    provider character varying(32) NOT NULL,
    slot_id integer NOT NULL,
    cycle character varying(16) NOT NULL,
    price_cents integer NOT NULL,
    currency character varying(8) DEFAULT 'USD'::character varying NOT NULL,
    external_plan_id character varying(128) NOT NULL,
    status character varying(32) DEFAULT 'ACTIVE'::character varying NOT NULL,
    raw_payload jsonb DEFAULT '{}'::jsonb,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    identity_client_id text,
    identity_environment text
);

CREATE SEQUENCE public.payment_provider_plans_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.payment_provider_plans_id_seq OWNED BY public.payment_provider_plans.id;

CREATE TABLE public.payment_refund_commands (
    id uuid NOT NULL,
    environment text NOT NULL,
    actor_id text,
    request_key uuid NOT NULL,
    order_id bigint NOT NULL,
    receipt_id bigint,
    amount_minor bigint,
    currency character varying(3) NOT NULL,
    status text NOT NULL,
    provider_refund_id text,
    legacy_source boolean DEFAULT false NOT NULL,
    first_sent_at timestamp with time zone,
    next_check_at timestamp with time zone,
    claimed_until timestamp with time zone,
    result_note text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    stop_loss_full_remaining boolean DEFAULT false NOT NULL,
    CONSTRAINT payment_refund_commands_amount_minor_check CHECK ((amount_minor > 0)),
    CONSTRAINT payment_refund_commands_check CHECK ((legacy_source OR ((actor_id IS NOT NULL) AND (receipt_id IS NOT NULL) AND (amount_minor IS NOT NULL)))),
    CONSTRAINT payment_refund_commands_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'confirming'::text, 'succeeded'::text, 'failed'::text, 'review'::text])))
);

CREATE TABLE public.payment_refund_failures (
    request_id uuid NOT NULL,
    order_id bigint NOT NULL,
    provider_refund_id character varying(100) NOT NULL,
    provider_status text NOT NULL,
    admin_id character varying(100),
    failed_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.payment_refunds (
    order_id bigint NOT NULL,
    request_id uuid NOT NULL,
    provider_refund_id character varying(100),
    status character varying(20) DEFAULT 'Pending'::character varying NOT NULL,
    admin_id character varying(100),
    charged_coin_shortfall integer DEFAULT 0 NOT NULL,
    bonus_coin_shortfall integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    prior_order_status character varying(20) DEFAULT 'Success'::character varying NOT NULL,
    requested_amount_cents integer,
    channel_lookup_only boolean DEFAULT false NOT NULL
);

CREATE TABLE public.payment_settlement_bank_reviews (
    id bigint NOT NULL,
    payout_id bigint NOT NULL,
    entry_id bigint NOT NULL,
    outcome text NOT NULL,
    operator text NOT NULL,
    reason text NOT NULL,
    manual_evidence text,
    checked_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_settlement_bank_reviews_operator_check CHECK ((btrim(operator) <> ''::text)),
    CONSTRAINT payment_settlement_bank_reviews_outcome_check CHECK ((outcome = ANY (ARRAY['matched'::text, 'amount_mismatch'::text, 'reference_mismatch'::text, 'not_ready'::text]))),
    CONSTRAINT payment_settlement_bank_reviews_reason_check CHECK ((btrim(reason) <> ''::text))
);

ALTER TABLE public.payment_settlement_bank_reviews ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_settlement_bank_reviews_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_settlement_commands (
    id uuid NOT NULL,
    request_key uuid NOT NULL,
    actor_id text NOT NULL,
    kind text NOT NULL,
    account_id bigint,
    environment text NOT NULL,
    reason text NOT NULL,
    parameters jsonb NOT NULL,
    upload bytea,
    request_digest text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    result jsonb,
    error text,
    lease_token uuid,
    lease_until timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_settlement_commands_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text]))),
    CONSTRAINT payment_settlement_commands_kind_check CHECK ((kind = ANY (ARRAY['paypal_report'::text, 'stripe_payout'::text, 'stripe_report'::text, 'bank_statement'::text, 'bank_match'::text]))),
    CONSTRAINT payment_settlement_commands_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text])))
);

CREATE TABLE public.payment_settlement_imports (
    id bigint NOT NULL,
    account_id bigint NOT NULL,
    source text NOT NULL,
    external_ref text NOT NULL,
    fingerprint bytea NOT NULL,
    operator text NOT NULL,
    reason text NOT NULL,
    imported_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_settlement_imports_fingerprint_check CHECK ((octet_length(fingerprint) = 32)),
    CONSTRAINT payment_settlement_imports_operator_check CHECK ((btrim(operator) <> ''::text)),
    CONSTRAINT payment_settlement_imports_reason_check CHECK ((btrim(reason) <> ''::text)),
    CONSTRAINT payment_settlement_imports_source_check CHECK ((source = ANY (ARRAY['paypal_settlement'::text, 'stripe_report'::text, 'stripe_payout'::text])))
);

ALTER TABLE public.payment_settlement_imports ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_settlement_imports_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_settlement_payouts (
    id bigint NOT NULL,
    account_id bigint NOT NULL,
    external_ref text NOT NULL,
    currency character varying(3) NOT NULL,
    expected_minor bigint NOT NULL,
    bank_reference text,
    expected_at timestamp with time zone,
    channel_status text NOT NULL,
    allocation_required boolean NOT NULL,
    allocation_complete boolean NOT NULL,
    transfer_key text,
    reversal_key text,
    last_observed_at timestamp with time zone NOT NULL,
    review_required boolean DEFAULT false NOT NULL,
    CONSTRAINT payment_settlement_payouts_channel_status_check CHECK ((channel_status = ANY (ARRAY['pending'::text, 'in_transit'::text, 'paid'::text, 'failed'::text, 'canceled'::text]))),
    CONSTRAINT payment_settlement_payouts_currency_check CHECK (((currency)::text = 'USD'::text)),
    CONSTRAINT payment_settlement_payouts_expected_minor_check CHECK ((expected_minor > 0))
);

CREATE VIEW public.payment_settlement_payout_checks AS
 SELECT p.id,
    p.account_id,
    p.external_ref,
    p.channel_status,
    p.currency,
    p.expected_minor,
    p.expected_at,
    p.bank_reference,
    p.allocation_required,
    p.allocation_complete,
    members.row_count,
    members.net_total_minor,
    debit.amount_minor AS transfer_minor,
        CASE
            WHEN (p.review_required OR transfer.review_required OR members.has_conflict) THEN 'review'::text
            WHEN ((p.channel_status = ANY (ARRAY['failed'::text, 'canceled'::text])) OR (p.reversal_key IS NOT NULL) OR ((a.provider = 'paypal'::text) AND (EXISTS ( SELECT 1
               FROM ((public.payment_ledger_references r
                 JOIN public.payment_ledger_movements reversal ON (((reversal.id = r.movement_id) AND (reversal.account_id = p.account_id))))
                 JOIN public.payment_ledger_references original ON (((original.movement_id = transfer.id) AND (original.object_type = 'paypal_payout_transfer'::text) AND (original.object_id = r.object_id))))
              WHERE ((r.object_type = 'paypal_payout'::text) AND (reversal.provider_code = 'T1101'::text) AND ((reversal.currency)::text = (p.currency)::text) AND (reversal.posted_at IS NOT NULL)))))) THEN 'reversed'::text
            WHEN (p.channel_status <> 'paid'::text) THEN 'awaiting_channel'::text
            WHEN ((debit.amount_minor IS NULL) OR (transfer.posted_at IS NULL)) THEN 'missing_transfer'::text
            WHEN (transfer.kind <> 'payout'::text) THEN 'review'::text
            WHEN ((debit.amount_minor)::numeric <> (- (p.expected_minor)::numeric)) THEN 'transfer_amount_mismatch'::text
            WHEN (p.allocation_required AND ((NOT p.allocation_complete) OR (members.row_count = 0) OR members.incomplete)) THEN 'missing_movements'::text
            WHEN (p.allocation_required AND (members.net_total_minor <> (p.expected_minor)::numeric)) THEN 'allocation_amount_mismatch'::text
            ELSE 'ready'::text
        END AS channel_check
   FROM ((((public.payment_settlement_payouts p
     JOIN public.payment_channel_accounts a ON ((a.id = p.account_id)))
     LEFT JOIN public.payment_ledger_movements transfer ON (((transfer.account_id = p.account_id) AND (transfer.movement_key = p.transfer_key))))
     LEFT JOIN public.payment_ledger_amounts debit ON (((debit.movement_id = transfer.id) AND (debit.component = 'gross'::text) AND ((debit.currency)::text = (p.currency)::text))))
     LEFT JOIN LATERAL ( SELECT count(*) AS row_count,
            sum(net.amount_minor) AS net_total_minor,
            COALESCE(bool_or(((net.amount_minor IS NULL) OR (m.posted_at IS NULL))), false) AS incomplete,
            COALESCE(bool_or((m.review_required OR ((m.currency)::text <> (p.currency)::text) OR (EXISTS ( SELECT 1
                   FROM public.payment_ledger_references another
                  WHERE ((another.movement_id = m.id) AND (another.object_type = ANY (ARRAY['stripe_payout'::text, 'stripe_payout_allocation'::text])) AND (another.object_id <> p.external_ref)))))), false) AS has_conflict
           FROM ((public.payment_ledger_references r
             JOIN public.payment_ledger_movements m ON (((m.id = r.movement_id) AND (m.account_id = p.account_id) AND (m.movement_key IS DISTINCT FROM p.transfer_key) AND (m.movement_key IS DISTINCT FROM p.reversal_key))))
             LEFT JOIN public.payment_ledger_amounts net ON (((net.movement_id = m.id) AND (net.component = 'net'::text) AND ((net.currency)::text = (p.currency)::text))))
          WHERE ((a.provider = 'stripe'::text) AND (r.object_type = 'stripe_payout_allocation'::text) AND (r.object_id = p.external_ref))) members ON (true));

CREATE TABLE public.payment_settlement_payout_evidence (
    payout_id bigint NOT NULL,
    import_id bigint NOT NULL,
    payload jsonb NOT NULL
);

ALTER TABLE public.payment_settlement_payouts ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_settlement_payouts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE VIEW public.payment_settlement_reconciliation AS
 SELECT p.id,
    p.account_id,
    p.external_ref,
    p.channel_status,
    p.currency,
    p.expected_minor,
    p.expected_at,
    p.bank_reference,
    p.allocation_required,
    p.allocation_complete,
    p.row_count,
    p.net_total_minor,
    p.transfer_minor,
    p.channel_check,
    bank.amount_minor AS bank_amount_minor,
    bank.currency AS bank_currency,
    bank.booked_at,
    bank.transaction_id AS bank_transaction_id,
    ((bank.amount_minor)::numeric - (p.expected_minor)::numeric) AS difference_minor,
        CASE
            WHEN ((matched.id IS NOT NULL) AND ((p.channel_check <> 'ready'::text) OR ((p.bank_reference IS NOT NULL) AND (bank.bank_reference <> p.bank_reference)))) THEN 'matched_requires_review'::text
            WHEN (matched.id IS NOT NULL) THEN 'matched'::text
            WHEN (p.channel_check <> 'ready'::text) THEN p.channel_check
            WHEN (candidate.outcome = ANY (ARRAY['amount_mismatch'::text, 'reference_mismatch'::text])) THEN candidate.outcome
            WHEN ((p.expected_at IS NOT NULL) AND (p.expected_at < date_trunc('day'::text, now(), 'UTC'::text))) THEN 'bank_delayed'::text
            ELSE 'awaiting_bank'::text
        END AS status
   FROM (((public.payment_settlement_payout_checks p
     LEFT JOIN public.payment_settlement_bank_reviews matched ON (((matched.payout_id = p.id) AND (matched.outcome = 'matched'::text))))
     LEFT JOIN LATERAL ( SELECT r.entry_id,
            r.outcome
           FROM public.payment_settlement_bank_reviews r
          WHERE (r.payout_id = p.id)
          ORDER BY r.id DESC
         LIMIT 1) candidate ON (true))
     LEFT JOIN public.payment_bank_statement_entries bank ON ((bank.id = COALESCE(matched.entry_id, candidate.entry_id))));

CREATE TABLE public.payment_settlement_rows (
    import_id bigint NOT NULL,
    source_file text NOT NULL,
    row_number integer NOT NULL,
    movement_id bigint,
    payload jsonb NOT NULL,
    review_reason text,
    withheld_tax_minor bigint,
    fee_net_of_withheld_tax_minor bigint,
    CONSTRAINT payment_settlement_rows_check CHECK (((movement_id IS NOT NULL) OR (review_reason IS NOT NULL))),
    CONSTRAINT payment_settlement_rows_row_number_check CHECK ((row_number > 0))
);

CREATE TABLE public.payment_stripe_checkout_terms (
    attempt_id uuid NOT NULL,
    product_ref text NOT NULL,
    price_ref text NOT NULL,
    amount_minor bigint NOT NULL,
    allowed_countries text[] NOT NULL,
    observed_country text,
    country_checked_at timestamp with time zone,
    ledger_observed_at timestamp with time zone,
    tax_liability_account text,
    observed_tax_liability_account text,
    observed_customer_tax_status text,
    validated_catalog jsonb,
    release_evidence text,
    market_policy text,
    expected_country text,
    market_basis text DEFAULT 'stripe_confirmation_required'::text NOT NULL,
    presentment_currency text,
    presentment_amount_minor bigint,
    settlement_state text DEFAULT 'none'::text NOT NULL,
    settlement_retry_until timestamp with time zone,
    settlement_movement_ref text,
    settlement_currency text,
    settlement_gross_minor bigint,
    settlement_fee_minor bigint,
    settlement_withheld_tax_minor bigint,
    settlement_net_minor bigint,
    settlement_exchange_rate text,
    frozen_checkout_params jsonb,
    CONSTRAINT payment_stripe_checkout_terms_amount_minor_check CHECK ((amount_minor > 0))
);

CREATE TABLE public.payment_stripe_customers (
    user_id bigint NOT NULL,
    account_id bigint NOT NULL,
    environment text NOT NULL,
    stripe_customer_id text NOT NULL,
    verified_billing_country text,
    tax_evidence_verified boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_stripe_customers_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text]))),
    CONSTRAINT payment_stripe_customers_stripe_customer_id_check CHECK ((stripe_customer_id ~ '^cus_[a-zA-Z0-9_-]+$'::text))
);

CREATE TABLE public.payment_stripe_invoice_allocations (
    account_id bigint NOT NULL,
    invoice_ref text NOT NULL,
    allocation_ref text NOT NULL,
    receipt_id bigint NOT NULL,
    amount_minor bigint NOT NULL,
    presentment_currency text,
    presentment_amount_minor bigint,
    settlement_movement_ref text,
    settlement_currency text,
    settlement_gross_minor bigint,
    settlement_fee_minor bigint,
    settlement_withheld_tax_minor bigint,
    settlement_net_minor bigint,
    settlement_exchange_rate text,
    CONSTRAINT payment_stripe_invoice_allocations_amount_minor_check CHECK ((amount_minor > 0))
);

CREATE TABLE public.payment_stripe_invoice_periods (
    account_id bigint NOT NULL,
    invoice_ref text NOT NULL,
    intent_id uuid NOT NULL,
    order_id bigint NOT NULL,
    service_start timestamp with time zone,
    service_end timestamp with time zone,
    billed_minor bigint NOT NULL,
    billing_reason text NOT NULL,
    country text,
    observed_tax_liability_account text,
    observed_customer_tax_status text,
    review_reason text,
    admitted boolean DEFAULT false NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    provider_status text,
    revises_invoice_ref text,
    revision_verified boolean DEFAULT false NOT NULL,
    settlement_state text DEFAULT 'none'::text NOT NULL,
    settlement_retry_until timestamp with time zone,
    settlement_next_check_at timestamp with time zone,
    settlement_verified_at timestamp with time zone,
    CONSTRAINT payment_stripe_invoice_periods_billed_minor_check CHECK ((billed_minor >= 0)),
    CONSTRAINT payment_stripe_invoice_periods_check CHECK (((NOT admitted) OR ((service_start IS NOT NULL) AND (service_end > service_start))))
);

CREATE TABLE public.payment_stripe_market_reviews (
    order_id bigint NOT NULL,
    evidence_key text NOT NULL,
    evidence jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    approved_by text,
    approval_reason text,
    approved_at timestamp with time zone
);

CREATE TABLE public.payment_stripe_one_time_offers (
    account_id bigint NOT NULL,
    slot_id integer NOT NULL,
    product_ref text NOT NULL,
    price_ref text NOT NULL,
    amount_minor bigint NOT NULL,
    allowed_countries text[] NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    sales_mode text DEFAULT 'direct'::text NOT NULL,
    tax_liability_account text,
    validated_catalog jsonb,
    release_evidence text,
    market_policy text,
    CONSTRAINT payment_stripe_one_time_offers_allowed_countries_check CHECK ((cardinality(allowed_countries) > 0)),
    CONSTRAINT payment_stripe_one_time_offers_amount_minor_check CHECK ((amount_minor > 0)),
    CONSTRAINT payment_stripe_one_time_offers_price_ref_check CHECK ((price_ref ~ '^price_[a-zA-Z0-9]+$'::text)),
    CONSTRAINT payment_stripe_one_time_offers_product_ref_check CHECK ((product_ref ~ '^prod_[a-zA-Z0-9]+$'::text)),
    CONSTRAINT payment_stripe_one_time_offers_sales_mode_check CHECK ((sales_mode = ANY (ARRAY['direct'::text, 'merchant_of_record'::text])))
);

CREATE TABLE public.payment_stripe_report_runs (
    id bigint NOT NULL,
    account_id bigint NOT NULL,
    environment text NOT NULL,
    report_type text NOT NULL,
    schedule_key text NOT NULL,
    params_digest text NOT NULL,
    parameters jsonb DEFAULT '{}'::jsonb NOT NULL,
    include_mor_columns boolean DEFAULT false NOT NULL,
    expected_columns text[] DEFAULT '{}'::text[] NOT NULL,
    idempotency_key text NOT NULL,
    report_run_id text,
    status text DEFAULT 'pending'::text NOT NULL,
    import_command_id uuid,
    actor_id text NOT NULL,
    reason text NOT NULL,
    last_error text,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_token uuid,
    lease_until timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT payment_stripe_report_runs_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text]))),
    CONSTRAINT payment_stripe_report_runs_idempotency_key_check CHECK ((btrim(idempotency_key) <> ''::text)),
    CONSTRAINT payment_stripe_report_runs_params_digest_check CHECK ((btrim(params_digest) <> ''::text)),
    CONSTRAINT payment_stripe_report_runs_reason_check CHECK ((btrim(reason) <> ''::text)),
    CONSTRAINT payment_stripe_report_runs_report_type_check CHECK ((btrim(report_type) <> ''::text)),
    CONSTRAINT payment_stripe_report_runs_schedule_key_check CHECK ((btrim(schedule_key) <> ''::text)),
    CONSTRAINT payment_stripe_report_runs_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'creating'::text, 'running'::text, 'succeeded'::text, 'enqueued'::text, 'failed'::text])))
);

ALTER TABLE public.payment_stripe_report_runs ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.payment_stripe_report_runs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);

CREATE TABLE public.payment_stripe_review_approvals (
    id uuid NOT NULL,
    case_id bigint NOT NULL,
    account_id bigint NOT NULL,
    environment text NOT NULL,
    review_id text NOT NULL,
    idempotency_key text NOT NULL,
    requested_by text NOT NULL,
    reason text NOT NULL,
    status text DEFAULT 'pending_confirmation'::text NOT NULL,
    closed_reason text DEFAULT ''::text NOT NULL,
    lookup_only boolean DEFAULT false NOT NULL,
    first_sent_at timestamp with time zone,
    retry_until timestamp with time zone,
    next_attempt_at timestamp with time zone DEFAULT now() NOT NULL,
    lease_token uuid,
    lease_until timestamp with time zone,
    last_error text,
    created_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    updated_at timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    finished_at timestamp with time zone,
    CONSTRAINT payment_stripe_review_approvals_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text]))),
    CONSTRAINT payment_stripe_review_approvals_idempotency_key_check CHECK ((btrim(idempotency_key) <> ''::text)),
    CONSTRAINT payment_stripe_review_approvals_reason_check CHECK ((btrim(reason) <> ''::text)),
    CONSTRAINT payment_stripe_review_approvals_review_id_check CHECK ((btrim(review_id) <> ''::text)),
    CONSTRAINT payment_stripe_review_approvals_status_check CHECK ((status = ANY (ARRAY['pending_confirmation'::text, 'confirmed'::text, 'failed'::text])))
);

CREATE TABLE public.payment_stripe_subscription_intents (
    id uuid NOT NULL,
    user_id bigint NOT NULL,
    account_id bigint NOT NULL,
    checkout_environment text NOT NULL,
    offer_version_id integer,
    slot_id integer NOT NULL,
    template_id integer NOT NULL,
    product_ref text NOT NULL,
    price_ref text NOT NULL,
    amount_minor bigint NOT NULL,
    cycle text NOT NULL,
    sales_mode text NOT NULL,
    allowed_countries text[] NOT NULL,
    tax_liability_account text,
    return_url text NOT NULL,
    cancel_url text NOT NULL,
    return_path text DEFAULT ''::text NOT NULL,
    attribution jsonb DEFAULT '{}'::jsonb NOT NULL,
    checkout_ref text,
    approval_url text,
    subscription_ref text,
    customer_ref text,
    status text DEFAULT 'creating'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    first_sent_at timestamp with time zone,
    retry_until timestamp with time zone,
    next_check_at timestamp with time zone DEFAULT now() NOT NULL,
    invoice_scan_cursor text,
    cancel_key uuid,
    cancel_first_sent_at timestamp with time zone,
    cancel_retry_until timestamp with time zone,
    cancel_next_at timestamp with time zone,
    cancelled_at timestamp with time zone,
    validated_catalog jsonb,
    release_evidence text,
    market_policy text,
    expected_country text,
    market_basis text DEFAULT 'stripe_confirmation_required'::text NOT NULL,
    provider_observed_at timestamp with time zone,
    create_lookup_only boolean DEFAULT false NOT NULL,
    cancel_lookup_only boolean DEFAULT false NOT NULL,
    product_id bigint,
    product_snapshot jsonb,
    frozen_checkout_params jsonb,
    CONSTRAINT payment_stripe_subscription_intents_amount_minor_check CHECK ((amount_minor > 0)),
    CONSTRAINT payment_stripe_subscription_intents_cycle_check CHECK ((cycle = ANY (ARRAY['day'::text, 'week'::text, 'month'::text, 'year'::text]))),
    CONSTRAINT payment_stripe_subscription_intents_sales_mode_check CHECK ((sales_mode = ANY (ARRAY['direct'::text, 'merchant_of_record'::text]))),
    CONSTRAINT payment_stripe_subscription_intents_status_check CHECK ((status = ANY (ARRAY['creating'::text, 'buyer_action'::text, 'bound'::text, 'closed'::text])))
);

CREATE TABLE public.payment_stripe_vip_offers (
    account_id bigint NOT NULL,
    offer_version_id integer NOT NULL,
    product_ref text NOT NULL,
    price_ref text NOT NULL,
    sales_mode text NOT NULL,
    allowed_countries text[] NOT NULL,
    tax_liability_account text,
    enabled boolean DEFAULT false NOT NULL,
    validated_catalog jsonb,
    release_evidence text,
    market_policy text,
    CONSTRAINT payment_stripe_vip_offers_allowed_countries_check CHECK ((cardinality(allowed_countries) > 0)),
    CONSTRAINT payment_stripe_vip_offers_price_ref_check CHECK ((price_ref ~ '^price_[a-zA-Z0-9]+$'::text)),
    CONSTRAINT payment_stripe_vip_offers_product_ref_check CHECK ((product_ref ~ '^prod_[a-zA-Z0-9]+$'::text)),
    CONSTRAINT payment_stripe_vip_offers_sales_mode_check CHECK ((sales_mode = ANY (ARRAY['direct'::text, 'merchant_of_record'::text])))
);

CREATE TABLE public.payment_task_metrics_schedule (
    singleton boolean NOT NULL,
    next_collection_at timestamp with time zone NOT NULL,
    CONSTRAINT payment_task_metrics_schedule_singleton_check CHECK (singleton)
);

CREATE TABLE public.payment_unattributed_counts (
    shard smallint NOT NULL,
    attempts integer NOT NULL,
    event_count bigint NOT NULL,
    CONSTRAINT payment_unattributed_counts_event_count_check CHECK ((event_count >= 0))
);

CREATE TABLE public.payment_vip_checkout_intents (
    intent_id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id bigint NOT NULL,
    product_id bigint NOT NULL,
    slot_id integer DEFAULT 0 NOT NULL,
    template_id integer DEFAULT 0 NOT NULL,
    price_cents integer NOT NULL,
    currency text DEFAULT 'USD'::text NOT NULL,
    cycle text NOT NULL,
    checkout_environment text NOT NULL,
    product_snapshot jsonb NOT NULL,
    return_path text DEFAULT ''::text NOT NULL,
    active_provider text,
    paypal_intent_id uuid,
    stripe_intent_id uuid,
    status text DEFAULT 'Pending'::text NOT NULL,
    subscription_id character varying(100),
    request_key uuid,
    attribution jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    switch_to_provider text,
    CONSTRAINT payment_vip_checkout_intents_active_provider_check CHECK (((active_provider IS NULL) OR (active_provider = ANY (ARRAY['paypal'::text, 'stripe'::text])))),
    CONSTRAINT payment_vip_checkout_intents_check CHECK (((switch_to_provider IS NULL) OR ((active_provider IS NOT NULL) AND (switch_to_provider = ANY (ARRAY['paypal'::text, 'stripe'::text])) AND (switch_to_provider <> active_provider)))),
    CONSTRAINT payment_vip_checkout_intents_checkout_environment_check CHECK ((checkout_environment = ANY (ARRAY['sandbox'::text, 'production'::text]))),
    CONSTRAINT payment_vip_checkout_intents_currency_check CHECK ((currency = 'USD'::text)),
    CONSTRAINT payment_vip_checkout_intents_cycle_check CHECK ((cycle = ANY (ARRAY['day'::text, 'week'::text, 'month'::text, 'year'::text]))),
    CONSTRAINT payment_vip_checkout_intents_price_cents_check CHECK ((price_cents > 0)),
    CONSTRAINT payment_vip_checkout_intents_status_check CHECK ((status = ANY (ARRAY['Pending'::text, 'Completed'::text, 'Canceled'::text, 'Expired'::text])))
);

CREATE TABLE public.payment_vip_offer_versions (
    slot_id integer NOT NULL,
    template_id integer NOT NULL,
    slot_index integer NOT NULL,
    amount_minor integer NOT NULL,
    currency character varying(3) NOT NULL,
    cycle text NOT NULL,
    name text NOT NULL,
    description text NOT NULL,
    CONSTRAINT payment_vip_offer_versions_amount_minor_check CHECK ((amount_minor > 0)),
    CONSTRAINT payment_vip_offer_versions_currency_check CHECK (((currency)::text ~ '^[A-Z]{3}$'::text)),
    CONSTRAINT payment_vip_offer_versions_cycle_check CHECK ((cycle = ANY (ARRAY['day'::text, 'week'::text, 'month'::text, 'year'::text])))
);

CREATE TABLE public.paypal_identity_lock (
    id boolean DEFAULT true NOT NULL,
    client_id text NOT NULL,
    environment text NOT NULL,
    CONSTRAINT paypal_identity_lock_id_check CHECK (id)
);

CREATE TABLE public.paypal_processed_events (
    id text NOT NULL,
    processed_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.paypal_subscription_billing_snapshots (
    subscription_id character varying(100) NOT NULL,
    paid_at timestamp with time zone NOT NULL,
    currency character varying(3) NOT NULL,
    amount_cents bigint NOT NULL,
    paid_period_end timestamp with time zone NOT NULL,
    CONSTRAINT paypal_subscription_billing_snapshots_amount_cents_check CHECK ((amount_cents > 0)),
    CONSTRAINT paypal_subscription_billing_snapshots_check CHECK ((paid_period_end > paid_at))
);

CREATE TABLE public.paypal_subscription_intents (
    user_id bigint NOT NULL,
    intent_id uuid NOT NULL,
    slot_id integer NOT NULL,
    template_id integer NOT NULL,
    cycle text NOT NULL,
    price_cents integer NOT NULL,
    return_url text NOT NULL,
    cancel_url text NOT NULL,
    plan_id text,
    subscription_id text,
    approval_url text,
    lease_until timestamp with time zone,
    submitted_at timestamp with time zone,
    attribution jsonb DEFAULT '{}'::jsonb NOT NULL,
    checkout_account_id bigint,
    checkout_return_path text DEFAULT ''::text NOT NULL,
    vip_offer_version_id integer,
    checkout_environment text,
    product_id bigint,
    product_snapshot jsonb,
    CONSTRAINT paypal_subscription_intents_checkout_environment_check CHECK ((checkout_environment = ANY (ARRAY['sandbox'::text, 'production'::text]))),
    CONSTRAINT paypal_subscription_intents_price_cents_check CHECK ((price_cents > 0))
);

CREATE TABLE public.product_channel_bindings (
    id bigint NOT NULL,
    product_id bigint NOT NULL,
    account_id bigint NOT NULL,
    provider text NOT NULL,
    environment text NOT NULL,
    external_product_ref text DEFAULT ''::text NOT NULL,
    external_price_ref text DEFAULT ''::text NOT NULL,
    request_key uuid DEFAULT gen_random_uuid() NOT NULL,
    first_sent_at timestamp with time zone,
    review_required boolean DEFAULT false NOT NULL,
    sales_mode text DEFAULT 'direct'::text NOT NULL,
    allowed_countries text[] DEFAULT ARRAY['US'::text] NOT NULL,
    tax_liability_account text,
    validated_catalog jsonb,
    release_evidence text,
    market_policy text,
    validation_status text DEFAULT 'pending'::text NOT NULL,
    validation_error text,
    validated_at timestamp with time zone,
    enabled boolean DEFAULT false NOT NULL,
    created_by character varying(36),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_by character varying(36),
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    catalog_revision integer DEFAULT 1 NOT NULL,
    CONSTRAINT product_channel_bindings_allowed_countries_check CHECK ((cardinality(allowed_countries) > 0)),
    CONSTRAINT product_channel_bindings_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text]))),
    CONSTRAINT product_channel_bindings_provider_check CHECK ((provider = ANY (ARRAY['paypal'::text, 'stripe'::text]))),
    CONSTRAINT product_channel_bindings_sales_mode_check CHECK ((sales_mode = ANY (ARRAY['direct'::text, 'merchant_of_record'::text]))),
    CONSTRAINT product_channel_bindings_validation_status_check CHECK ((validation_status = ANY (ARRAY['pending'::text, 'verified'::text, 'failed'::text, 'review'::text])))
);

CREATE SEQUENCE public.product_channel_bindings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.product_channel_bindings_id_seq OWNED BY public.product_channel_bindings.id;

CREATE TABLE public.product_publications (
    product_id bigint NOT NULL,
    environment text NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    published_at timestamp with time zone,
    published_by character varying(36),
    paused_at timestamp with time zone,
    retired_at timestamp with time zone,
    updated_by character varying(36),
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT product_publications_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text]))),
    CONSTRAINT product_publications_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'published'::text, 'paused'::text, 'retired'::text])))
);

CREATE TABLE public.products (
    id bigint NOT NULL,
    code text NOT NULL,
    source_product_id bigint,
    product_type text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    price_cents integer NOT NULL,
    currency text DEFAULT 'USD'::text NOT NULL,
    coins integer DEFAULT 0 NOT NULL,
    bonus integer DEFAULT 0 NOT NULL,
    vip_cycle text,
    novel_scope_type text DEFAULT 'none'::text NOT NULL,
    novel_scope_ids bigint[] DEFAULT '{}'::bigint[] NOT NULL,
    created_by character varying(36),
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chk_products_single_rules CHECK (((product_type <> 'single'::text) OR ((coins > 0) AND ((bonus * 10) <= (coins * 15)) AND (vip_cycle IS NULL) AND (novel_scope_type = 'none'::text) AND (cardinality(novel_scope_ids) = 0)))),
    CONSTRAINT chk_products_vip_rules CHECK (((product_type <> 'vip'::text) OR ((coins = 0) AND (bonus = 0) AND (vip_cycle = ANY (ARRAY['day'::text, 'week'::text, 'month'::text, 'year'::text])) AND (novel_scope_type = 'none'::text) AND (cardinality(novel_scope_ids) = 0)))),
    CONSTRAINT chk_products_whole_book_rules CHECK (((product_type <> 'whole_book'::text) OR ((coins = 0) AND (bonus = 0) AND (vip_cycle IS NULL) AND (novel_scope_type = ANY (ARRAY['all_published'::text, 'promoted_novel'::text, 'specific_novels'::text])) AND (((novel_scope_type = 'specific_novels'::text) AND (cardinality(novel_scope_ids) > 0)) OR ((novel_scope_type <> 'specific_novels'::text) AND (cardinality(novel_scope_ids) = 0)))))),
    CONSTRAINT products_bonus_check CHECK ((bonus >= 0)),
    CONSTRAINT products_code_check CHECK ((btrim(code) <> ''::text)),
    CONSTRAINT products_coins_check CHECK ((coins >= 0)),
    CONSTRAINT products_currency_check CHECK ((currency = 'USD'::text)),
    CONSTRAINT products_name_check CHECK ((btrim(name) <> ''::text)),
    CONSTRAINT products_novel_scope_type_check CHECK ((novel_scope_type = ANY (ARRAY['none'::text, 'all_published'::text, 'promoted_novel'::text, 'specific_novels'::text]))),
    CONSTRAINT products_price_cents_check CHECK ((price_cents > 0)),
    CONSTRAINT products_product_type_check CHECK ((product_type = ANY (ARRAY['single'::text, 'whole_book'::text, 'vip'::text]))),
    CONSTRAINT products_vip_cycle_check CHECK (((vip_cycle IS NULL) OR (vip_cycle = ANY (ARRAY['day'::text, 'week'::text, 'month'::text, 'year'::text]))))
);

CREATE SEQUENCE public.products_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.products_id_seq OWNED BY public.products.id;

CREATE SEQUENCE public.promotion_links_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.promotion_links_id_seq OWNED BY public.promotion_links.id;

CREATE TABLE public.reader_conversion_outbox (
    business_key text NOT NULL,
    event_name text NOT NULL,
    order_id bigint,
    intent_id text,
    occurred_at timestamp with time zone,
    snapshot jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    consumed_at timestamp with time zone,
    environment text DEFAULT 'unknown'::text NOT NULL,
    processing_error text
);

CREATE TABLE public.reader_conversion_repair_facts (
    business_key text NOT NULL,
    event_name text NOT NULL,
    order_id bigint NOT NULL,
    environment text NOT NULL,
    occurred_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    snapshot jsonb NOT NULL,
    normalized_at timestamp with time zone,
    original_send_started_at timestamp with time zone
);

CREATE TABLE public.reader_payment_first_on_promotion (
    user_id bigint NOT NULL,
    promotion_link_id integer NOT NULL,
    order_id bigint NOT NULL
);

CREATE TABLE public.reader_payment_promotion_coverage (
    environment text NOT NULL,
    promotion_link_id integer NOT NULL,
    verified_at timestamp with time zone NOT NULL,
    evidence_reference text NOT NULL,
    CONSTRAINT reader_payment_promotion_coverage_evidence_reference_check CHECK ((evidence_reference <> ''::text))
);

CREATE TABLE public.reader_schema_migrations (
    version bigint NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.reader_sessions (
    token_hash text NOT NULL,
    user_id bigint NOT NULL,
    expires_at timestamp with time zone NOT NULL
);

CREATE SEQUENCE public.recharge_slots_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.recharge_slots_id_seq OWNED BY public.recharge_slots.id;

CREATE TABLE public.recharge_templates (
    id integer NOT NULL,
    name character varying(100) NOT NULL,
    is_default boolean DEFAULT false,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    environment text DEFAULT 'live'::text NOT NULL,
    CONSTRAINT recharge_templates_environment_check CHECK ((environment = ANY (ARRAY['sandbox'::text, 'live'::text])))
);

CREATE SEQUENCE public.recharge_templates_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.recharge_templates_id_seq OWNED BY public.recharge_templates.id;

CREATE TABLE public.recommendations (
    id integer NOT NULL,
    group_name character varying(50) NOT NULL,
    novel_id integer NOT NULL,
    sort_order integer DEFAULT 0,
    scheduled_start timestamp with time zone,
    scheduled_end timestamp with time zone,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

CREATE SEQUENCE public.recommendations_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.recommendations_id_seq OWNED BY public.recommendations.id;

CREATE TABLE public.role_permissions (
    role_id bigint NOT NULL,
    permission_code character varying(100) NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE public.roles (
    id bigint NOT NULL,
    code character varying(64) NOT NULL,
    name character varying(100) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    is_system boolean DEFAULT false NOT NULL,
    is_super_admin boolean DEFAULT false NOT NULL,
    status smallint DEFAULT 1 NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE SEQUENCE public.roles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.roles_id_seq OWNED BY public.roles.id;

CREATE TABLE public.system_configs (
    key character varying(100) NOT NULL,
    value character varying(255) NOT NULL
);

CREATE TABLE public.system_domains (
    id integer NOT NULL,
    name character varying(255) NOT NULL,
    domain character varying(255) NOT NULL,
    type character varying(20) DEFAULT 'sub'::character varying NOT NULL,
    status smallint DEFAULT 1,
    is_default boolean DEFAULT false,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

CREATE SEQUENCE public.system_domains_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.system_domains_id_seq OWNED BY public.system_domains.id;

CREATE SEQUENCE public.third_party_payment_orders_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.third_party_payment_orders_id_seq OWNED BY public.third_party_payment_orders.id;

CREATE TABLE public.transactions (
    id character varying(36) NOT NULL,
    user_id bigint NOT NULL,
    type character varying(10) NOT NULL,
    biz_type character varying(20) NOT NULL,
    amount integer NOT NULL,
    charged_amount integer DEFAULT 0,
    bonus_amount integer DEFAULT 0,
    description character varying(255),
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE public.unlock_records (
    id bigint NOT NULL,
    user_id bigint NOT NULL,
    novel_id integer NOT NULL,
    chapter_index integer NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    chapter_id character varying(64),
    identity_evidence text,
    identity_resolved_at timestamp with time zone
);

CREATE SEQUENCE public.unlock_records_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.unlock_records_id_seq OWNED BY public.unlock_records.id;

CREATE SEQUENCE public.user_subscriptions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.user_subscriptions_id_seq OWNED BY public.user_subscriptions.id;

CREATE TABLE public.users (
    id bigint NOT NULL,
    email character varying(255),
    password_hash character varying(255),
    nickname character varying(50),
    avatar_url character varying(255),
    status smallint DEFAULT 1,
    device character varying(255) DEFAULT ''::character varying,
    ip_address character varying(45) DEFAULT ''::character varying,
    utm_source character varying(100) DEFAULT ''::character varying,
    utm_campaign character varying(100) DEFAULT ''::character varying,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP,
    promotion_link_id integer,
    promotion_resolved boolean DEFAULT false NOT NULL
);

CREATE SEQUENCE public.users_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;

CREATE TABLE public.wallets (
    user_id bigint NOT NULL,
    charged_coins integer DEFAULT 0,
    bonus_coins integer DEFAULT 0,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE ONLY public.admin_audit_logs ALTER COLUMN id SET DEFAULT nextval('public.admin_audit_logs_id_seq'::regclass);

ALTER TABLE ONLY public.bookshelves ALTER COLUMN id SET DEFAULT nextval('public.bookshelves_id_seq'::regclass);

ALTER TABLE ONLY public.fb_pixels ALTER COLUMN id SET DEFAULT nextval('public.fb_pixels_id_seq'::regclass);

ALTER TABLE ONLY public.feedback ALTER COLUMN id SET DEFAULT nextval('public.feedback_id_seq'::regclass);

ALTER TABLE ONLY public.gift_code_redemptions ALTER COLUMN id SET DEFAULT nextval('public.gift_code_redemptions_id_seq'::regclass);

ALTER TABLE ONLY public.gift_codes ALTER COLUMN id SET DEFAULT nextval('public.gift_codes_id_seq'::regclass);

ALTER TABLE ONLY public.payment_channel_credentials ALTER COLUMN id SET DEFAULT nextval('public.payment_channel_credentials_id_seq'::regclass);

ALTER TABLE ONLY public.payment_provider_plans ALTER COLUMN id SET DEFAULT nextval('public.payment_provider_plans_id_seq'::regclass);

ALTER TABLE ONLY public.product_channel_bindings ALTER COLUMN id SET DEFAULT nextval('public.product_channel_bindings_id_seq'::regclass);

ALTER TABLE ONLY public.products ALTER COLUMN id SET DEFAULT nextval('public.products_id_seq'::regclass);

ALTER TABLE ONLY public.promotion_links ALTER COLUMN id SET DEFAULT nextval('public.promotion_links_id_seq'::regclass);

ALTER TABLE ONLY public.recharge_slots ALTER COLUMN id SET DEFAULT nextval('public.recharge_slots_id_seq'::regclass);

ALTER TABLE ONLY public.recharge_templates ALTER COLUMN id SET DEFAULT nextval('public.recharge_templates_id_seq'::regclass);

ALTER TABLE ONLY public.recommendations ALTER COLUMN id SET DEFAULT nextval('public.recommendations_id_seq'::regclass);

ALTER TABLE ONLY public.roles ALTER COLUMN id SET DEFAULT nextval('public.roles_id_seq'::regclass);

ALTER TABLE ONLY public.system_domains ALTER COLUMN id SET DEFAULT nextval('public.system_domains_id_seq'::regclass);

ALTER TABLE ONLY public.third_party_payment_orders ALTER COLUMN id SET DEFAULT nextval('public.third_party_payment_orders_id_seq'::regclass);

ALTER TABLE ONLY public.unlock_records ALTER COLUMN id SET DEFAULT nextval('public.unlock_records_id_seq'::regclass);

ALTER TABLE ONLY public.user_subscriptions ALTER COLUMN id SET DEFAULT nextval('public.user_subscriptions_id_seq'::regclass);

ALTER TABLE ONLY public.users ALTER COLUMN id SET DEFAULT nextval('public.users_id_seq'::regclass);

ALTER TABLE ONLY conversion.alerts
    ADD CONSTRAINT alerts_pkey PRIMARY KEY (environment, kind, subject);

ALTER TABLE ONLY conversion.collector_samples
    ADD CONSTRAINT collector_samples_pkey PRIMARY KEY (environment);

ALTER TABLE ONLY conversion.credentials
    ADD CONSTRAINT credentials_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.deliveries
    ADD CONSTRAINT deliveries_event_id_destination_id_key UNIQUE (event_id, destination_id);

ALTER TABLE ONLY conversion.deliveries
    ADD CONSTRAINT deliveries_id_event_id_role_key UNIQUE (id, event_id, role);

ALTER TABLE ONLY conversion.deliveries
    ADD CONSTRAINT deliveries_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.delivery_attempts
    ADD CONSTRAINT delivery_attempts_batch_id_batch_position_key UNIQUE (batch_id, batch_position);

ALTER TABLE ONLY conversion.delivery_attempts
    ADD CONSTRAINT delivery_attempts_batch_id_delivery_id_key UNIQUE (batch_id, delivery_id);

ALTER TABLE ONLY conversion.delivery_attempts
    ADD CONSTRAINT delivery_attempts_pkey PRIMARY KEY (delivery_id, attempt_no);

ALTER TABLE ONLY conversion.delivery_diagnostics
    ADD CONSTRAINT delivery_diagnostics_pkey PRIMARY KEY (delivery_id, attempt_no, late);

ALTER TABLE ONLY conversion.destinations
    ADD CONSTRAINT destinations_environment_media_pixel_id_key UNIQUE (environment, media, pixel_id);

ALTER TABLE ONLY conversion.destinations
    ADD CONSTRAINT destinations_id_environment_key UNIQUE (id, environment);

ALTER TABLE ONLY conversion.destinations
    ADD CONSTRAINT destinations_legacy_fb_pixel_id_key UNIQUE (legacy_fb_pixel_id);

ALTER TABLE ONLY conversion.destinations
    ADD CONSTRAINT destinations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.events
    ADD CONSTRAINT events_id_environment_key UNIQUE (id, environment);

ALTER TABLE ONLY conversion.events
    ADD CONSTRAINT events_id_integration_id_key UNIQUE (id, integration_id);

ALTER TABLE ONLY conversion.events
    ADD CONSTRAINT events_integration_id_source_event_type_source_event_id_key UNIQUE (integration_id, source_event_type, source_event_id);

ALTER TABLE ONLY conversion.events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.inbox
    ADD CONSTRAINT inbox_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.integration_destinations
    ADD CONSTRAINT integration_destinations_pkey PRIMARY KEY (integration_id, destination_id);

ALTER TABLE ONLY conversion.integrations
    ADD CONSTRAINT integrations_environment_source_account_key_key UNIQUE (environment, source, account_key);

ALTER TABLE ONLY conversion.integrations
    ADD CONSTRAINT integrations_id_environment_key UNIQUE (id, environment);

ALTER TABLE ONLY conversion.integrations
    ADD CONSTRAINT integrations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.legacy_outbox_imports
    ADD CONSTRAINT legacy_outbox_imports_event_id_key UNIQUE (event_id);

ALTER TABLE ONLY conversion.legacy_outbox_imports
    ADD CONSTRAINT legacy_outbox_imports_pkey PRIMARY KEY (legacy_event_id);

ALTER TABLE ONLY conversion.meta_batches
    ADD CONSTRAINT meta_batches_id_confirmed_key UNIQUE (id, confirmed);

ALTER TABLE ONLY conversion.meta_batches
    ADD CONSTRAINT meta_batches_id_lease_token_key UNIQUE (id, lease_token);

ALTER TABLE ONLY conversion.meta_batches
    ADD CONSTRAINT meta_batches_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.operation_audit
    ADD CONSTRAINT operation_audit_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.partner_report_attempts
    ADD CONSTRAINT partner_report_attempts_pkey PRIMARY KEY (report_id, attempt_no);

ALTER TABLE ONLY conversion.partner_reports
    ADD CONSTRAINT partner_reports_integration_id_event_id_original_delivery_i_key UNIQUE (integration_id, event_id, original_delivery_id);

ALTER TABLE ONLY conversion.partner_reports
    ADD CONSTRAINT partner_reports_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.payment_policy_counters
    ADD CONSTRAINT payment_policy_counters_pkey PRIMARY KEY (environment, integration_id, app_scope, scope_kind, promotion_key, branch_id);

ALTER TABLE ONLY conversion.payment_policy_decisions
    ADD CONSTRAINT payment_policy_decisions_environment_integration_id_app_sco_key UNIQUE (environment, integration_id, app_scope, payment_key);

ALTER TABLE ONLY conversion.payment_policy_decisions
    ADD CONSTRAINT payment_policy_decisions_id_environment_integration_id_app__key UNIQUE (id, environment, integration_id, app_scope, payment_key);

ALTER TABLE ONLY conversion.payment_policy_decisions
    ADD CONSTRAINT payment_policy_decisions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.payment_policy_scopes
    ADD CONSTRAINT payment_policy_scopes_environment_integration_id_app_scope__key UNIQUE (environment, integration_id, app_scope, scope_kind, promotion_key);

ALTER TABLE ONLY conversion.payment_policy_scopes
    ADD CONSTRAINT payment_policy_scopes_id_environment_integration_id_app_sco_key UNIQUE (id, environment, integration_id, app_scope);

ALTER TABLE ONLY conversion.payment_policy_scopes
    ADD CONSTRAINT payment_policy_scopes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.payment_policy_versions
    ADD CONSTRAINT payment_policy_versions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY conversion.payment_policy_versions
    ADD CONSTRAINT payment_policy_versions_scope_id_id_key UNIQUE (scope_id, id);

ALTER TABLE ONLY conversion.payment_policy_versions
    ADD CONSTRAINT payment_policy_versions_scope_id_version_no_key UNIQUE (scope_id, version_no);

ALTER TABLE ONLY conversion.worker_heartbeats
    ADD CONSTRAINT worker_heartbeats_pkey PRIMARY KEY (environment, worker_type, instance_id);

ALTER TABLE ONLY public.admin_audit_logs
    ADD CONSTRAINT admin_audit_logs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.admins
    ADD CONSTRAINT admins_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.admins
    ADD CONSTRAINT admins_username_key UNIQUE (username);

ALTER TABLE ONLY public.bookshelves
    ADD CONSTRAINT bookshelves_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.chapter_content_objects
    ADD CONSTRAINT chapter_content_objects_pkey PRIMARY KEY (object_key);

ALTER TABLE ONLY public.chapter_entitlement_links
    ADD CONSTRAINT chapter_entitlement_links_pkey PRIMARY KEY (chapter_id);

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapter_id_novel_unique UNIQUE (id, novel_id);

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapters_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.fb_pixels
    ADD CONSTRAINT fb_pixels_pixel_id_key UNIQUE (pixel_id);

ALTER TABLE ONLY public.fb_pixels
    ADD CONSTRAINT fb_pixels_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.feedback
    ADD CONSTRAINT feedback_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.gift_code_redemptions
    ADD CONSTRAINT gift_code_redemptions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.gift_codes
    ADD CONSTRAINT gift_codes_code_key UNIQUE (code);

ALTER TABLE ONLY public.gift_codes
    ADD CONSTRAINT gift_codes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.legacy_whole_book_proofs
    ADD CONSTRAINT legacy_whole_book_proofs_pkey PRIMARY KEY (order_id);

ALTER TABLE ONLY public.novels
    ADD CONSTRAINT novels_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_aftercare_policies
    ADD CONSTRAINT payment_aftercare_policies_pkey PRIMARY KEY (account_id);

ALTER TABLE ONLY public.payment_bank_statement_entries
    ADD CONSTRAINT payment_bank_statement_entrie_environment_bank_account_tran_key UNIQUE (environment, bank_account, transaction_id);

ALTER TABLE ONLY public.payment_bank_statement_entries
    ADD CONSTRAINT payment_bank_statement_entries_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_case_audit
    ADD CONSTRAINT payment_case_audit_pkey PRIMARY KEY (case_id, request_id);

ALTER TABLE ONLY public.payment_case_evidence
    ADD CONSTRAINT payment_case_evidence_pkey PRIMARY KEY (case_id, source, source_ref, digest);

ALTER TABLE ONLY public.payment_case_reminders
    ADD CONSTRAINT payment_case_reminders_pkey PRIMARY KEY (case_id, due_at);

ALTER TABLE ONLY public.payment_cases
    ADD CONSTRAINT payment_cases_account_id_kind_external_id_key UNIQUE (account_id, kind, external_id);

ALTER TABLE ONLY public.payment_cases
    ADD CONSTRAINT payment_cases_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_channel_account_versions
    ADD CONSTRAINT payment_channel_account_versions_pkey PRIMARY KEY (account_id, version);

ALTER TABLE ONLY public.payment_channel_accounts
    ADD CONSTRAINT payment_channel_accounts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_channel_accounts
    ADD CONSTRAINT payment_channel_accounts_provider_environment_account_key_key UNIQUE (provider, environment, account_key);

ALTER TABLE ONLY public.payment_channel_accounts
    ADD CONSTRAINT payment_channel_accounts_provider_id_key UNIQUE (provider, id);

ALTER TABLE ONLY public.payment_channel_credentials
    ADD CONSTRAINT payment_channel_credentials_account_id_version_purpose_key UNIQUE (account_id, version, purpose);

ALTER TABLE ONLY public.payment_channel_credentials
    ADD CONSTRAINT payment_channel_credentials_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_checkout_attempts
    ADD CONSTRAINT payment_checkout_attempts_account_id_provider_order_id_key UNIQUE (account_id, provider_order_id);

ALTER TABLE ONLY public.payment_checkout_attempts
    ADD CONSTRAINT payment_checkout_attempts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_checkout_decisions
    ADD CONSTRAINT payment_checkout_decisions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_checkout_requests
    ADD CONSTRAINT payment_checkout_requests_pkey PRIMARY KEY (environment, user_id, request_key);

ALTER TABLE ONLY public.payment_deferred_fulfillment
    ADD CONSTRAINT payment_deferred_fulfillment_pkey PRIMARY KEY (order_id, receipt_id);

ALTER TABLE ONLY public.payment_deferred_fulfillment
    ADD CONSTRAINT payment_deferred_fulfillment_receipt_id_key UNIQUE (receipt_id);

ALTER TABLE ONLY public.payment_entitlement_adjustments
    ADD CONSTRAINT payment_entitlement_adjustments_pkey PRIMARY KEY (order_id);

ALTER TABLE ONLY public.payment_event_inbox
    ADD CONSTRAINT payment_event_identity UNIQUE NULLS NOT DISTINCT (provider, account_id, event_id);

ALTER TABLE ONLY public.payment_event_inbox
    ADD CONSTRAINT payment_event_inbox_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_event_outbox
    ADD CONSTRAINT payment_event_outbox_pkey PRIMARY KEY (event_id);

ALTER TABLE ONLY public.payment_event_recovery_failures
    ADD CONSTRAINT payment_event_recovery_failures_pkey PRIMARY KEY (account_id, event_id);

ALTER TABLE ONLY public.payment_event_recovery_groups
    ADD CONSTRAINT payment_event_recovery_groups_pkey PRIMARY KEY (account_id, group_key);

ALTER TABLE ONLY public.payment_event_recovery
    ADD CONSTRAINT payment_event_recovery_pkey PRIMARY KEY (account_id);

ALTER TABLE ONLY public.payment_fulfillment_funding
    ADD CONSTRAINT payment_fulfillment_funding_pkey PRIMARY KEY (order_id, receipt_id);

ALTER TABLE ONLY public.payment_ledger_amounts
    ADD CONSTRAINT payment_ledger_amounts_pkey PRIMARY KEY (movement_id, component);

ALTER TABLE ONLY public.payment_ledger_evidence
    ADD CONSTRAINT payment_ledger_evidence_movement_id_id_key UNIQUE (movement_id, id);

ALTER TABLE ONLY public.payment_ledger_evidence
    ADD CONSTRAINT payment_ledger_evidence_movement_id_source_source_ref_paylo_key UNIQUE (movement_id, source, source_ref, payload_hash);

ALTER TABLE ONLY public.payment_ledger_evidence
    ADD CONSTRAINT payment_ledger_evidence_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_ledger_movements
    ADD CONSTRAINT payment_ledger_movements_account_id_movement_key_key UNIQUE (account_id, movement_key);

ALTER TABLE ONLY public.payment_ledger_movements
    ADD CONSTRAINT payment_ledger_movements_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_ledger_references
    ADD CONSTRAINT payment_ledger_references_pkey PRIMARY KEY (movement_id, object_type, object_id);

ALTER TABLE ONLY public.payment_ledger_sync_settings
    ADD CONSTRAINT payment_ledger_sync_settings_pkey PRIMARY KEY (account_id);

ALTER TABLE ONLY public.payment_loss_commands
    ADD CONSTRAINT payment_loss_commands_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_paypal_vip_plans
    ADD CONSTRAINT payment_paypal_vip_plans_pkey PRIMARY KEY (slot_id, account_id);

ALTER TABLE ONLY public.payment_paypal_vip_plans
    ADD CONSTRAINT payment_paypal_vip_plans_request_key_key UNIQUE (request_key);

ALTER TABLE ONLY public.payment_provider_plans
    ADD CONSTRAINT payment_provider_plans_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.third_party_payment_orders
    ADD CONSTRAINT payment_receipt_order_key UNIQUE (order_id, id);

ALTER TABLE ONLY public.payment_refund_commands
    ADD CONSTRAINT payment_refund_commands_environment_actor_id_request_key_key UNIQUE NULLS NOT DISTINCT (environment, actor_id, request_key);

ALTER TABLE ONLY public.payment_refund_commands
    ADD CONSTRAINT payment_refund_commands_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_refund_commands
    ADD CONSTRAINT payment_refund_commands_provider_refund_id_key UNIQUE (provider_refund_id);

ALTER TABLE ONLY public.payment_refund_failures
    ADD CONSTRAINT payment_refund_failures_pkey PRIMARY KEY (request_id);

ALTER TABLE ONLY public.payment_refunds
    ADD CONSTRAINT payment_refunds_pkey PRIMARY KEY (order_id);

ALTER TABLE ONLY public.payment_refunds
    ADD CONSTRAINT payment_refunds_provider_refund_id_key UNIQUE (provider_refund_id);

ALTER TABLE ONLY public.payment_refunds
    ADD CONSTRAINT payment_refunds_request_id_key UNIQUE (request_id);

ALTER TABLE ONLY public.payment_settlement_bank_reviews
    ADD CONSTRAINT payment_settlement_bank_reviews_payout_id_entry_id_outcome_key UNIQUE (payout_id, entry_id, outcome);

ALTER TABLE ONLY public.payment_settlement_bank_reviews
    ADD CONSTRAINT payment_settlement_bank_reviews_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_settlement_commands
    ADD CONSTRAINT payment_settlement_commands_actor_id_request_key_key UNIQUE (actor_id, request_key);

ALTER TABLE ONLY public.payment_settlement_commands
    ADD CONSTRAINT payment_settlement_commands_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_settlement_imports
    ADD CONSTRAINT payment_settlement_imports_account_id_source_external_ref_f_key UNIQUE (account_id, source, external_ref, fingerprint);

ALTER TABLE ONLY public.payment_settlement_imports
    ADD CONSTRAINT payment_settlement_imports_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_settlement_payout_evidence
    ADD CONSTRAINT payment_settlement_payout_evidence_pkey PRIMARY KEY (payout_id, import_id);

ALTER TABLE ONLY public.payment_settlement_payouts
    ADD CONSTRAINT payment_settlement_payouts_account_id_external_ref_key UNIQUE (account_id, external_ref);

ALTER TABLE ONLY public.payment_settlement_payouts
    ADD CONSTRAINT payment_settlement_payouts_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_settlement_rows
    ADD CONSTRAINT payment_settlement_rows_pkey PRIMARY KEY (import_id, source_file, row_number);

ALTER TABLE ONLY public.payment_stripe_checkout_terms
    ADD CONSTRAINT payment_stripe_checkout_terms_pkey PRIMARY KEY (attempt_id);

ALTER TABLE ONLY public.payment_stripe_customers
    ADD CONSTRAINT payment_stripe_customers_account_id_environment_stripe_cust_key UNIQUE (account_id, environment, stripe_customer_id);

ALTER TABLE ONLY public.payment_stripe_customers
    ADD CONSTRAINT payment_stripe_customers_pkey PRIMARY KEY (user_id, account_id, environment);

ALTER TABLE ONLY public.payment_stripe_invoice_allocations
    ADD CONSTRAINT payment_stripe_invoice_allocations_pkey PRIMARY KEY (account_id, allocation_ref);

ALTER TABLE ONLY public.payment_stripe_invoice_periods
    ADD CONSTRAINT payment_stripe_invoice_periods_order_id_key UNIQUE (order_id);

ALTER TABLE ONLY public.payment_stripe_invoice_periods
    ADD CONSTRAINT payment_stripe_invoice_periods_pkey PRIMARY KEY (account_id, invoice_ref);

ALTER TABLE ONLY public.payment_stripe_market_reviews
    ADD CONSTRAINT payment_stripe_market_reviews_pkey PRIMARY KEY (order_id, evidence_key);

ALTER TABLE ONLY public.payment_stripe_one_time_offers
    ADD CONSTRAINT payment_stripe_one_time_offers_pkey PRIMARY KEY (account_id, slot_id);

ALTER TABLE ONLY public.payment_stripe_refund_items
    ADD CONSTRAINT payment_stripe_refund_items_pkey PRIMARY KEY (account_id, refund_ref);

ALTER TABLE ONLY public.payment_stripe_report_runs
    ADD CONSTRAINT payment_stripe_report_runs_account_id_environment_report_ty_key UNIQUE (account_id, environment, report_type, params_digest);

ALTER TABLE ONLY public.payment_stripe_report_runs
    ADD CONSTRAINT payment_stripe_report_runs_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_stripe_report_runs
    ADD CONSTRAINT payment_stripe_report_runs_schedule_key_key UNIQUE (schedule_key);

ALTER TABLE ONLY public.payment_stripe_review_approvals
    ADD CONSTRAINT payment_stripe_review_approva_account_id_environment_review_key UNIQUE (account_id, environment, review_id);

ALTER TABLE ONLY public.payment_stripe_review_approvals
    ADD CONSTRAINT payment_stripe_review_approvals_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_stripe_subscription_intents
    ADD CONSTRAINT payment_stripe_subscription_int_account_id_subscription_ref_key UNIQUE (account_id, subscription_ref);

ALTER TABLE ONLY public.payment_stripe_subscription_intents
    ADD CONSTRAINT payment_stripe_subscription_intents_account_id_checkout_ref_key UNIQUE (account_id, checkout_ref);

ALTER TABLE ONLY public.payment_stripe_subscription_intents
    ADD CONSTRAINT payment_stripe_subscription_intents_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_stripe_vip_offers
    ADD CONSTRAINT payment_stripe_vip_offers_pkey PRIMARY KEY (account_id, offer_version_id);

ALTER TABLE ONLY public.payment_task_metrics_schedule
    ADD CONSTRAINT payment_task_metrics_schedule_pkey PRIMARY KEY (singleton);

ALTER TABLE ONLY public.payment_unattributed_counts
    ADD CONSTRAINT payment_unattributed_counts_pkey PRIMARY KEY (shard, attempts);

ALTER TABLE ONLY public.payment_vip_checkout_intents
    ADD CONSTRAINT payment_vip_checkout_intents_pkey PRIMARY KEY (intent_id);

ALTER TABLE ONLY public.payment_vip_offer_versions
    ADD CONSTRAINT payment_vip_offer_versions_pkey PRIMARY KEY (slot_id);

ALTER TABLE ONLY public.payment_vip_offer_versions
    ADD CONSTRAINT payment_vip_offer_versions_template_id_slot_index_amount_mi_key UNIQUE (template_id, slot_index, amount_minor, currency, cycle, name, description);

ALTER TABLE ONLY public.paypal_identity_lock
    ADD CONSTRAINT paypal_identity_lock_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.paypal_processed_events
    ADD CONSTRAINT paypal_processed_events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.paypal_refund_items
    ADD CONSTRAINT paypal_refund_items_pkey PRIMARY KEY (refund_id);

ALTER TABLE ONLY public.paypal_subscription_billing_snapshots
    ADD CONSTRAINT paypal_subscription_billing_snapshots_pkey PRIMARY KEY (subscription_id, paid_at, currency, amount_cents);

ALTER TABLE ONLY public.paypal_subscription_intents
    ADD CONSTRAINT paypal_subscription_intents_intent_id_key UNIQUE (intent_id);

ALTER TABLE ONLY public.paypal_subscription_intents
    ADD CONSTRAINT paypal_subscription_intents_pkey PRIMARY KEY (intent_id);

ALTER TABLE ONLY public.paypal_subscription_intents
    ADD CONSTRAINT paypal_subscription_intents_subscription_id_key UNIQUE (subscription_id);

ALTER TABLE ONLY public.product_channel_bindings
    ADD CONSTRAINT product_channel_bindings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.product_channel_bindings
    ADD CONSTRAINT product_channel_bindings_product_id_account_id_key UNIQUE (product_id, account_id);

ALTER TABLE ONLY public.product_publications
    ADD CONSTRAINT product_publications_pkey PRIMARY KEY (product_id, environment);

ALTER TABLE ONLY public.products
    ADD CONSTRAINT products_code_key UNIQUE (code);

ALTER TABLE ONLY public.products
    ADD CONSTRAINT products_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.promotion_links
    ADD CONSTRAINT promotion_links_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.reader_conversion_outbox
    ADD CONSTRAINT reader_conversion_outbox_pkey PRIMARY KEY (business_key);

ALTER TABLE ONLY public.reader_conversion_repair_facts
    ADD CONSTRAINT reader_conversion_repair_facts_pkey PRIMARY KEY (business_key);

ALTER TABLE ONLY public.reader_payment_first_on_promotion
    ADD CONSTRAINT reader_payment_first_on_promotion_pkey PRIMARY KEY (user_id, promotion_link_id);

ALTER TABLE ONLY public.reader_payment_promotion_coverage
    ADD CONSTRAINT reader_payment_promotion_coverage_pkey PRIMARY KEY (environment, promotion_link_id);

ALTER TABLE ONLY public.reader_schema_migrations
    ADD CONSTRAINT reader_schema_migrations_pkey PRIMARY KEY (version);

ALTER TABLE ONLY public.reader_sessions
    ADD CONSTRAINT reader_sessions_pkey PRIMARY KEY (token_hash);

ALTER TABLE ONLY public.recharge_orders
    ADD CONSTRAINT recharge_orders_external_ref_id_key UNIQUE (external_ref_id);

ALTER TABLE ONLY public.recharge_orders
    ADD CONSTRAINT recharge_orders_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.recharge_slots
    ADD CONSTRAINT recharge_slots_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.recharge_templates
    ADD CONSTRAINT recharge_templates_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.recommendations
    ADD CONSTRAINT recommendations_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.role_permissions
    ADD CONSTRAINT role_permissions_pkey PRIMARY KEY (role_id, permission_code);

ALTER TABLE ONLY public.roles
    ADD CONSTRAINT roles_code_key UNIQUE (code);

ALTER TABLE ONLY public.roles
    ADD CONSTRAINT roles_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.system_configs
    ADD CONSTRAINT system_configs_pkey PRIMARY KEY (key);

ALTER TABLE ONLY public.system_domains
    ADD CONSTRAINT system_domains_domain_key UNIQUE (domain);

ALTER TABLE ONLY public.system_domains
    ADD CONSTRAINT system_domains_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.third_party_payment_orders
    ADD CONSTRAINT third_party_payment_orders_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.transactions
    ADD CONSTRAINT transactions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payment_provider_plans
    ADD CONSTRAINT uk_provider_slot UNIQUE (provider, slot_id);

ALTER TABLE ONLY public.recommendations
    ADD CONSTRAINT unique_group_novel UNIQUE (group_name, novel_id);

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT unique_novel_chapter_index UNIQUE (novel_id, chapter_index);

ALTER TABLE ONLY public.recharge_slots
    ADD CONSTRAINT unique_template_slot UNIQUE (template_id, slot_index);

ALTER TABLE ONLY public.third_party_payment_orders
    ADD CONSTRAINT unique_tp_provider_external_capture UNIQUE (payment_provider, external_order_id, capture_id);

ALTER TABLE ONLY public.gift_code_redemptions
    ADD CONSTRAINT unique_user_gift_code UNIQUE (user_id, gift_code_id);

ALTER TABLE ONLY public.bookshelves
    ADD CONSTRAINT unique_user_novel UNIQUE (user_id, novel_id);

ALTER TABLE ONLY public.unlock_records
    ADD CONSTRAINT unlock_records_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_checkout_intent_id_key UNIQUE (checkout_intent_id);

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_subscription_id_key UNIQUE (subscription_id);

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.wallets
    ADD CONSTRAINT wallets_pkey PRIMARY KEY (user_id);

CREATE INDEX conversion_attempt_recent_limits ON conversion.delivery_attempts USING btree (responded_at) WHERE (reason = 'meta_rate_limited'::text);

CREATE INDEX conversion_audit_subject ON conversion.operation_audit USING btree (environment, action, subject_id, id DESC);

CREATE INDEX conversion_deliveries_deadline ON conversion.deliveries USING btree (environment, deadline_at, id) WHERE (finalized_at IS NULL);

CREATE INDEX conversion_deliveries_destination ON conversion.deliveries USING btree (destination_id, state, id);

CREATE INDEX conversion_deliveries_lease ON conversion.deliveries USING btree (environment, lease_until, id) WHERE (lease_until IS NOT NULL);

CREATE UNIQUE INDEX conversion_deliveries_original ON conversion.deliveries USING btree (event_id) WHERE (role = 'original'::text);

CREATE INDEX conversion_deliveries_pending ON conversion.deliveries USING btree (environment, next_attempt_at, id) WHERE ((state = 'pending'::text) AND (lease_until IS NULL));

CREATE INDEX conversion_delivery_diagnostics_expiry ON conversion.delivery_diagnostics USING btree (recorded_at);

CREATE INDEX conversion_delivery_pixel_ready ON conversion.deliveries USING btree (destination_id, next_attempt_at, id) WHERE ((state = 'pending'::text) AND (lease_until IS NULL));

CREATE INDEX conversion_delivery_recent_failures ON conversion.deliveries USING btree (environment, finalized_at) WHERE (state = 'failed'::text);

CREATE INDEX conversion_events_created ON conversion.events USING btree (created_at DESC, id DESC);

CREATE INDEX conversion_events_integration_order ON conversion.events USING btree (integration_id, source_order_id);

CREATE INDEX conversion_events_integration_time ON conversion.events USING btree (integration_id, occurred_at);

CREATE UNIQUE INDEX conversion_events_partner_payment_key ON conversion.events USING btree (integration_id, environment, payment_app_scope, source_order_id) WHERE ((source_event_type = 'recharge-paid'::text) AND (payment_policy_state = ANY (ARRAY['pending'::text, 'decided'::text])));

CREATE INDEX conversion_events_partner_payment_lookup ON conversion.events USING btree (integration_id, environment, payment_app_scope, source_order_id, id) WHERE (source_event_type = 'recharge-paid'::text);

CREATE UNIQUE INDEX conversion_events_payment_decision_name ON conversion.events USING btree (payment_policy_decision_id, event_name) WHERE (payment_policy_decision_id IS NOT NULL);

CREATE INDEX conversion_events_promotion ON conversion.events USING btree (promotion_id, created_at DESC) WHERE (promotion_id IS NOT NULL);

CREATE INDEX conversion_events_reader_payment_policy_pending ON conversion.events USING btree (integration_id, environment, payment_app_scope, source_order_id, id) WHERE (payment_policy_state = 'pending'::text);

CREATE INDEX conversion_inbox_integration_order ON conversion.inbox USING btree (integration_id, source_order_id, received_at DESC, id DESC) WHERE (source_order_id IS NOT NULL);

CREATE INDEX conversion_inbox_normalized_event ON conversion.inbox USING btree (normalized_event_id, integration_id) WHERE (normalized_event_id IS NOT NULL);

CREATE INDEX conversion_inbox_pending ON conversion.inbox USING btree (received_at, id) WHERE (state = 'pending'::text);

CREATE INDEX conversion_inbox_processed_at ON conversion.inbox USING btree (integration_id, processed_at) WHERE (processed_at IS NOT NULL);

CREATE UNIQUE INDEX conversion_inbox_source_event ON conversion.inbox USING btree (integration_id, source_event_type, source_event_id) WHERE (state <> 'quarantined'::text);

CREATE UNIQUE INDEX conversion_meta_batch_active_pixel ON conversion.meta_batches USING btree (destination_id) WHERE (finished_at IS NULL);

CREATE INDEX conversion_meta_batch_expiry ON conversion.meta_batches USING btree (environment, lease_until, id) WHERE (finished_at IS NULL);

CREATE INDEX conversion_meta_batch_started ON conversion.meta_batches USING btree (environment, started_at) WHERE (started_at IS NOT NULL);

CREATE INDEX conversion_partner_reports_leased ON conversion.partner_reports USING btree (lease_until, id) WHERE (lease_until IS NOT NULL);

CREATE INDEX conversion_partner_reports_pending ON conversion.partner_reports USING btree (next_attempt_at, id) WHERE ((state = 'pending'::text) AND (lease_until IS NULL));

CREATE INDEX payment_policy_decisions_environment_time ON conversion.payment_policy_decisions USING btree (environment, decided_at, id);

CREATE INDEX chapter_content_cleanup_due ON public.chapter_content_objects USING btree (next_attempt_at) WHERE (state = ANY (ARRAY['staged'::text, 'retired'::text, 'deleting'::text]));

CREATE INDEX idx_admin_audit_logs_admin ON public.admin_audit_logs USING btree (admin_id);

CREATE INDEX idx_admins_role_id ON public.admins USING btree (role_id);

CREATE INDEX idx_bookshelves_user ON public.bookshelves USING btree (user_id);

CREATE INDEX idx_chapters_novel_index ON public.chapters USING btree (novel_id, chapter_index);

CREATE INDEX idx_checkout_decisions_order ON public.payment_checkout_decisions USING btree (order_id, id DESC) WHERE (order_id IS NOT NULL);

CREATE INDEX idx_checkout_decisions_review ON public.payment_checkout_decisions USING btree (checkout_environment, id DESC) WHERE (outcome = ANY (ARRAY['block'::text, 'verify'::text, 'review'::text]));

CREATE INDEX idx_checkout_decisions_user ON public.payment_checkout_decisions USING btree (user_id, id DESC);

CREATE INDEX idx_feedback_status ON public.feedback USING btree (status);

CREATE INDEX idx_feedback_user ON public.feedback USING btree (user_id);

CREATE INDEX idx_gift_code_redemptions_user ON public.gift_code_redemptions USING btree (user_id);

CREATE INDEX idx_live_vip_offer_version ON public.recharge_slots USING btree (vip_offer_version_id) WHERE (vip_offer_version_id IS NOT NULL);

CREATE INDEX idx_novels_author_trgm ON public.novels USING gin (author public.gin_trgm_ops);

CREATE INDEX idx_novels_genres_gin ON public.novels USING gin (genres);

CREATE INDEX idx_novels_status_publish_created ON public.novels USING btree (status, publish_status, created_at DESC, id DESC);

CREATE INDEX idx_novels_status_publish_views ON public.novels USING btree (status, publish_status, view_count DESC, created_at DESC);

CREATE INDEX idx_novels_synopsis_trgm ON public.novels USING gin (synopsis public.gin_trgm_ops);

CREATE INDEX idx_novels_title_trgm ON public.novels USING gin (title public.gin_trgm_ops);

CREATE INDEX idx_payment_cases_account_kind ON public.payment_cases USING btree (account_id, kind, id DESC);

CREATE INDEX idx_payment_cases_dispute_due ON public.payment_cases USING btree (due_at, id) WHERE ((kind = 'dispute'::text) AND is_open);

CREATE INDEX idx_payment_cases_receipts ON public.payment_cases USING gin (receipt_refs);

CREATE INDEX idx_payment_cases_work ON public.payment_cases USING btree (work_due_at, id) WHERE (work_state = 'pending'::text);

CREATE UNIQUE INDEX idx_payment_channel_accounts_single_primary ON public.payment_channel_accounts USING btree (provider, environment) WHERE (is_primary = true);

CREATE UNIQUE INDEX idx_payment_channel_accounts_webhook_token ON public.payment_channel_accounts USING btree (webhook_token);

CREATE UNIQUE INDEX idx_payment_checkout_active ON public.payment_checkout_attempts USING btree (order_id) WHERE (status = ANY (ARRAY['creating'::text, 'buyer_action'::text, 'capturing'::text]));

CREATE INDEX idx_payment_deferred_due ON public.payment_deferred_fulfillment USING btree (next_check_at) WHERE (resolved_at IS NULL);

CREATE INDEX idx_payment_event_recovery_groups_due ON public.payment_event_recovery_groups USING btree (account_id, next_attempt_at);

CREATE INDEX idx_payment_inbox_due ON public.payment_event_inbox USING btree (provider, next_attempt_at, id) WHERE (processed_at IS NULL);

CREATE INDEX idx_payment_ledger_reference_lookup ON public.payment_ledger_references USING btree (object_type, object_id, movement_id);

CREATE UNIQUE INDEX idx_payment_loss_pending ON public.payment_loss_commands USING btree (order_id) WHERE (status = 'pending'::text);

CREATE UNIQUE INDEX idx_payment_receipt_identity ON public.third_party_payment_orders USING btree (account_id, object_type, capture_id) WHERE (account_id IS NOT NULL);

CREATE INDEX idx_payment_recovery_failure_retry ON public.payment_event_recovery_failures USING btree (account_id, next_attempt_at) WHERE ((resolved_at IS NULL) AND (next_attempt_at IS NOT NULL));

CREATE INDEX idx_payment_refund_commands_due ON public.payment_refund_commands USING btree (next_check_at, id) WHERE (next_check_at IS NOT NULL);

CREATE INDEX idx_payment_refund_commands_legacy_order ON public.payment_refund_commands USING btree (order_id) WHERE ((receipt_id IS NULL) AND (status = 'review'::text));

CREATE INDEX idx_payment_refund_commands_order ON public.payment_refund_commands USING btree (order_id, created_at DESC);

CREATE INDEX idx_payment_refund_commands_receipt ON public.payment_refund_commands USING btree (receipt_id, created_at DESC);

CREATE INDEX idx_payment_settlement_commands_due ON public.payment_settlement_commands USING btree (environment, created_at) WHERE (status = ANY (ARRAY['pending'::text, 'running'::text]));

CREATE INDEX idx_payment_stripe_report_runs_due ON public.payment_stripe_report_runs USING btree (environment, next_attempt_at) WHERE (status = ANY (ARRAY['pending'::text, 'creating'::text, 'running'::text, 'succeeded'::text]));

CREATE UNIQUE INDEX idx_payment_stripe_report_runs_run_id ON public.payment_stripe_report_runs USING btree (account_id, environment, report_run_id) WHERE (report_run_id IS NOT NULL);

CREATE INDEX idx_paypal_cancel_due ON public.user_subscriptions USING btree (cancel_next_attempt_at, id) WHERE (((payment_method)::text = 'paypal'::text) AND ((status)::text = 'CANCEL_PENDING'::text));

CREATE INDEX idx_paypal_checkout_create_due ON public.payment_checkout_attempts USING btree (next_create_at, id) WHERE ((provider = 'paypal'::text) AND (status = 'creating'::text));

CREATE UNIQUE INDEX idx_paypal_checkout_order ON public.payment_checkout_attempts USING btree (provider_order_id) WHERE ((provider = 'paypal'::text) AND (provider_order_id IS NOT NULL));

CREATE INDEX idx_paypal_inbox_refund_capture ON public.payment_event_inbox USING btree (capture_id, amount_cents, currency) WHERE (event_type = 'refund_completed'::text);

CREATE INDEX idx_paypal_pending_reconciliation ON public.recharge_orders USING btree (COALESCE(reconciliation_at, created_at)) WHERE (((payment_method)::text = 'paypal'::text) AND ((status)::text = 'Pending'::text) AND (external_ref_id IS NOT NULL) AND (checkout_id IS NOT NULL));

CREATE INDEX idx_paypal_refund_capture ON public.paypal_refund_items USING btree (capture_id, currency);

CREATE INDEX idx_paypal_subscription_due ON public.user_subscriptions USING btree (paypal_next_check_at, id) WHERE ((payment_method)::text = 'paypal'::text);

CREATE UNIQUE INDEX idx_paypal_verified_capture ON public.third_party_payment_orders USING btree (capture_id) WHERE (((payment_provider)::text = 'paypal'::text) AND ((status)::text = 'VERIFIED'::text));

CREATE UNIQUE INDEX idx_paypal_vip_pending_user_environment ON public.paypal_subscription_intents USING btree (user_id, checkout_environment) NULLS NOT DISTINCT;

CREATE INDEX idx_paypal_vip_plan_pending ON public.payment_paypal_vip_plans USING btree (account_id, slot_id) WHERE ((plan_id IS NULL) AND (first_sent_at IS NULL) AND (NOT review_required));

CREATE INDEX idx_product_channel_bindings_lookup ON public.product_channel_bindings USING btree (environment, provider, product_id, enabled, validation_status);

CREATE INDEX idx_product_publications_env_status ON public.product_publications USING btree (environment, status, product_id);

CREATE INDEX idx_promotion_links_environment ON public.promotion_links USING btree (environment);

CREATE INDEX idx_provider_plans_lookup ON public.payment_provider_plans USING btree (provider, cycle, price_cents);

CREATE INDEX idx_provider_plans_slot ON public.payment_provider_plans USING btree (slot_id);

CREATE UNIQUE INDEX idx_recharge_checkout_id ON public.recharge_orders USING btree (checkout_id);

CREATE INDEX idx_recharge_orders_status ON public.recharge_orders USING btree (status);

CREATE INDEX idx_recharge_orders_user ON public.recharge_orders USING btree (user_id);

CREATE INDEX idx_recharge_slots_product ON public.recharge_slots USING btree (product_id);

CREATE INDEX idx_recommendations_group ON public.recommendations USING btree (group_name);

CREATE INDEX idx_role_permissions_code ON public.role_permissions USING btree (permission_code);

CREATE UNIQUE INDEX idx_settlement_matched_bank_entry ON public.payment_settlement_bank_reviews USING btree (entry_id) WHERE (outcome = 'matched'::text);

CREATE UNIQUE INDEX idx_settlement_matched_payout ON public.payment_settlement_bank_reviews USING btree (payout_id) WHERE (outcome = 'matched'::text);

CREATE INDEX idx_settlement_rows_movement ON public.payment_settlement_rows USING btree (movement_id) WHERE (movement_id IS NOT NULL);

CREATE UNIQUE INDEX idx_stripe_admitted_invoice_period ON public.payment_stripe_invoice_periods USING btree (intent_id, service_start, service_end) WHERE admitted;

CREATE INDEX idx_stripe_checkout_due ON public.payment_checkout_attempts USING btree (account_id, next_create_at, id) WHERE ((provider = 'stripe'::text) AND (status = ANY (ARRAY['creating'::text, 'buyer_action'::text, 'capturing'::text])));

CREATE INDEX idx_stripe_checkout_inbox_due ON public.payment_event_inbox USING btree (account_id, next_attempt_at, id) WHERE ((provider = 'stripe'::text) AND (processed_at IS NULL));

CREATE INDEX idx_stripe_checkout_pending_settlement ON public.payment_stripe_checkout_terms USING btree (attempt_id) WHERE (settlement_state = 'pending_settlement'::text);

CREATE INDEX idx_stripe_market_review_recent ON public.payment_stripe_market_reviews USING btree (order_id, updated_at DESC);

CREATE INDEX idx_stripe_refunds_receipt ON public.payment_stripe_refund_items USING btree (receipt_id);

CREATE INDEX idx_stripe_review_approvals_case ON public.payment_stripe_review_approvals USING btree (case_id);

CREATE INDEX idx_stripe_review_approvals_due ON public.payment_stripe_review_approvals USING btree (account_id, next_attempt_at) WHERE (status = 'pending_confirmation'::text);

CREATE INDEX idx_stripe_vip_cancel_due ON public.payment_stripe_subscription_intents USING btree (account_id, cancel_next_at) WHERE ((cancel_next_at IS NOT NULL) AND (cancelled_at IS NULL));

CREATE INDEX idx_stripe_vip_due ON public.payment_stripe_subscription_intents USING btree (account_id, next_check_at, id) WHERE (status = ANY (ARRAY['creating'::text, 'buyer_action'::text, 'bound'::text]));

CREATE INDEX idx_stripe_vip_invoice_intent ON public.payment_stripe_invoice_periods USING btree (intent_id, service_start, order_id);

CREATE INDEX idx_stripe_vip_invoice_pending_settlement ON public.payment_stripe_invoice_periods USING btree (account_id, settlement_next_check_at, invoice_ref) WHERE (settlement_state = 'pending_settlement'::text);

CREATE UNIQUE INDEX idx_stripe_vip_pending_user_environment ON public.payment_stripe_subscription_intents USING btree (user_id, checkout_environment) WHERE (status = ANY (ARRAY['creating'::text, 'buyer_action'::text]));

CREATE INDEX idx_tp_payment_capture_id ON public.third_party_payment_orders USING btree (capture_id);

CREATE INDEX idx_tp_payment_ext_id ON public.third_party_payment_orders USING btree (external_order_id);

CREATE INDEX idx_tp_payment_order_id ON public.third_party_payment_orders USING btree (order_id);

CREATE INDEX idx_transactions_user ON public.transactions USING btree (user_id);

CREATE UNIQUE INDEX idx_unique_user_daily_checkin ON public.transactions USING btree (user_id, (((created_at AT TIME ZONE 'UTC'::text))::date)) WHERE ((biz_type)::text = 'checkin'::text);

CREATE INDEX idx_unlock_records_user_novel ON public.unlock_records USING btree (user_id, novel_id);

CREATE INDEX idx_user_subscriptions_status ON public.user_subscriptions USING btree (status);

CREATE INDEX idx_user_subscriptions_sub_id ON public.user_subscriptions USING btree (subscription_id);

CREATE INDEX idx_user_subscriptions_user ON public.user_subscriptions USING btree (user_id);

CREATE UNIQUE INDEX idx_vip_checkout_intents_pending_user_env ON public.payment_vip_checkout_intents USING btree (user_id, checkout_environment) WHERE (status = 'Pending'::text);

CREATE UNIQUE INDEX idx_vip_checkout_intents_req ON public.payment_vip_checkout_intents USING btree (checkout_environment, user_id, request_key) WHERE (request_key IS NOT NULL);

CREATE INDEX idx_whole_book_checkout_pending ON public.recharge_orders USING btree (user_id, novel_id, id) WHERE (((order_type)::text = 'whole_book'::text) AND ((status)::text = 'Pending'::text));

CREATE INDEX idx_whole_book_entitlement ON public.recharge_orders(user_id, novel_id)
    WHERE order_type = 'whole_book' AND status IN ('Success','RefundPending') AND fulfilled_at IS NOT NULL;

CREATE INDEX payment_event_outbox_confirmed_order ON public.payment_event_outbox USING btree (order_id) WHERE ((state = 'delivered'::text) OR (delivered_at IS NOT NULL));

CREATE INDEX payment_event_outbox_due ON public.payment_event_outbox USING btree (next_attempt_at) WHERE (delivered_at IS NULL);

CREATE INDEX reader_conversion_outbox_order ON public.reader_conversion_outbox USING btree (order_id) WHERE (order_id IS NOT NULL);

CREATE INDEX reader_conversion_outbox_pending ON public.reader_conversion_outbox USING btree (environment, created_at, business_key) WHERE (consumed_at IS NULL);

CREATE INDEX reader_sessions_expiry ON public.reader_sessions USING btree (expires_at);

CREATE INDEX unlock_records_unresolved ON public.unlock_records USING btree (user_id, novel_id) WHERE (chapter_id IS NULL);

CREATE UNIQUE INDEX unlock_records_user_chapter ON public.unlock_records USING btree (user_id, chapter_id);

CREATE TRIGGER conversion_inbox_processed_at BEFORE INSERT OR UPDATE OF state ON conversion.inbox FOR EACH ROW EXECUTE FUNCTION conversion.stamp_inbox_processed_at();

CREATE TRIGGER conversion_original_send_started AFTER UPDATE OF sending_at ON conversion.delivery_attempts FOR EACH ROW WHEN (((old.sending_at IS NULL) AND (new.sending_at IS NOT NULL))) EXECUTE FUNCTION conversion.mark_original_send_started();

CREATE CONSTRAINT TRIGGER require_complete_payment_policy_decision AFTER INSERT ON conversion.payment_policy_decisions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN ((new.outcome IS NULL)) EXECUTE FUNCTION conversion.require_complete_payment_policy_decision();

CREATE TRIGGER chapter_content_reference AFTER INSERT OR DELETE OR UPDATE OF content_object_key, chapter_index, content ON public.chapters FOR EACH ROW EXECUTE FUNCTION public.track_chapter_content_reference();

CREATE TRIGGER discard_unfulfilled_legacy_conversion AFTER UPDATE OF status, fulfilled_at ON public.recharge_orders FOR EACH ROW WHEN ((((new.status)::text = 'Refunded'::text) AND (new.fulfilled_at IS NULL) AND (((new.status)::text IS DISTINCT FROM (old.status)::text) OR (new.fulfilled_at IS DISTINCT FROM old.fulfilled_at)))) EXECUTE FUNCTION public.discard_unfulfilled_legacy_conversion();

CREATE TRIGGER guard_promotion_pixel BEFORE INSERT OR UPDATE OF fb_pixel_id ON public.promotion_links FOR EACH ROW EXECUTE FUNCTION conversion.guard_promotion_pixel();

CREATE TRIGGER lock_reader_first_payment BEFORE INSERT OR UPDATE OF fulfilled_at ON public.recharge_orders FOR EACH ROW EXECUTE FUNCTION public.lock_reader_first_payment();

CREATE TRIGGER mark_cancel_requested BEFORE UPDATE ON public.user_subscriptions FOR EACH ROW EXECUTE FUNCTION public.mark_cancel_requested();

CREATE TRIGGER payment_unattributed_count AFTER INSERT OR DELETE OR UPDATE OF state, order_id, attempts ON public.payment_event_outbox FOR EACH ROW EXECUTE FUNCTION public.track_payment_unattributed_count();

CREATE TRIGGER paypal_identity_settings BEFORE INSERT OR DELETE OR UPDATE ON public.system_configs FOR EACH ROW EXECUTE FUNCTION public.guard_paypal_identity_settings();

CREATE TRIGGER paypal_subscription_status_change BEFORE UPDATE OF status ON public.user_subscriptions FOR EACH ROW EXECUTE FUNCTION public.track_paypal_subscription_status_change();

CREATE TRIGGER reader_body_revision BEFORE UPDATE OF content, content_object_key, content_sha256 ON public.chapters FOR EACH ROW EXECUTE FUNCTION public.advance_reader_body_revision();

CREATE TRIGGER reader_chapter_delete_version AFTER DELETE ON public.chapters REFERENCING OLD TABLE AS old_chapters FOR EACH STATEMENT EXECUTE FUNCTION public.bump_reader_chapter_statement();

CREATE TRIGGER reader_chapter_insert_version AFTER INSERT ON public.chapters REFERENCING NEW TABLE AS new_chapters FOR EACH STATEMENT EXECUTE FUNCTION public.bump_reader_chapter_statement();

CREATE TRIGGER reader_chapter_update_version AFTER UPDATE ON public.chapters REFERENCING OLD TABLE AS old_chapters NEW TABLE AS new_chapters FOR EACH STATEMENT EXECUTE FUNCTION public.bump_reader_chapter_statement();

CREATE TRIGGER reader_novel_version BEFORE UPDATE ON public.novels FOR EACH ROW EXECUTE FUNCTION public.bump_reader_novel_version();

CREATE TRIGGER record_paypal_channel_account AFTER INSERT OR UPDATE OF client_id, environment ON public.paypal_identity_lock FOR EACH ROW EXECUTE FUNCTION public.record_paypal_channel_account();

CREATE TRIGGER trg_product_bindings_protect_active BEFORE DELETE ON public.product_channel_bindings FOR EACH ROW EXECUTE FUNCTION public.forbid_active_product_binding_delete();

CREATE TRIGGER trg_product_publications_protect_non_draft BEFORE DELETE ON public.product_publications FOR EACH ROW EXECUTE FUNCTION public.forbid_non_draft_publication_delete();

CREATE TRIGGER trg_products_immutable BEFORE DELETE OR UPDATE ON public.products FOR EACH ROW EXECUTE FUNCTION public.forbid_product_mutation();

CREATE TRIGGER unlock_identity_guard BEFORE INSERT OR UPDATE ON public.unlock_records FOR EACH ROW EXECUTE FUNCTION public.guard_unlock_chapter_identity();

ALTER TABLE ONLY conversion.deliveries
    ADD CONSTRAINT deliveries_destination_id_environment_fkey FOREIGN KEY (destination_id, environment) REFERENCES conversion.destinations(id, environment);

ALTER TABLE ONLY conversion.deliveries
    ADD CONSTRAINT deliveries_event_id_environment_fkey FOREIGN KEY (event_id, environment) REFERENCES conversion.events(id, environment);

ALTER TABLE ONLY conversion.delivery_attempts
    ADD CONSTRAINT delivery_attempts_batch_id_batch_confirmed_fkey FOREIGN KEY (batch_id, batch_confirmed) REFERENCES conversion.meta_batches(id, confirmed);

ALTER TABLE ONLY conversion.delivery_attempts
    ADD CONSTRAINT delivery_attempts_batch_id_lease_token_fkey FOREIGN KEY (batch_id, lease_token) REFERENCES conversion.meta_batches(id, lease_token);

ALTER TABLE ONLY conversion.delivery_attempts
    ADD CONSTRAINT delivery_attempts_delivery_id_fkey FOREIGN KEY (delivery_id) REFERENCES conversion.deliveries(id);

ALTER TABLE ONLY conversion.delivery_diagnostics
    ADD CONSTRAINT delivery_diagnostics_delivery_id_attempt_no_fkey FOREIGN KEY (delivery_id, attempt_no) REFERENCES conversion.delivery_attempts(delivery_id, attempt_no);

ALTER TABLE ONLY conversion.destinations
    ADD CONSTRAINT destinations_blocked_credential_id_fkey FOREIGN KEY (blocked_credential_id) REFERENCES conversion.credentials(id);

ALTER TABLE ONLY conversion.destinations
    ADD CONSTRAINT destinations_credential_id_fkey FOREIGN KEY (credential_id) REFERENCES conversion.credentials(id);

ALTER TABLE ONLY conversion.events
    ADD CONSTRAINT events_integration_id_environment_fkey FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations(id, environment);

ALTER TABLE ONLY conversion.events
    ADD CONSTRAINT events_payment_policy_decision_id_environment_integration__fkey FOREIGN KEY (payment_policy_decision_id, environment, integration_id, payment_app_scope, source_order_id) REFERENCES conversion.payment_policy_decisions(id, environment, integration_id, app_scope, payment_key);

ALTER TABLE ONLY conversion.inbox
    ADD CONSTRAINT inbox_integration_id_fkey FOREIGN KEY (integration_id) REFERENCES conversion.integrations(id);

ALTER TABLE ONLY conversion.inbox
    ADD CONSTRAINT inbox_normalized_event_id_integration_id_fkey FOREIGN KEY (normalized_event_id, integration_id) REFERENCES conversion.events(id, integration_id);

ALTER TABLE ONLY conversion.integration_destinations
    ADD CONSTRAINT integration_destinations_destination_id_environment_fkey FOREIGN KEY (destination_id, environment) REFERENCES conversion.destinations(id, environment);

ALTER TABLE ONLY conversion.integration_destinations
    ADD CONSTRAINT integration_destinations_integration_id_environment_fkey FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations(id, environment);

ALTER TABLE ONLY conversion.integrations
    ADD CONSTRAINT integrations_inbound_credential_id_fkey FOREIGN KEY (inbound_credential_id) REFERENCES conversion.credentials(id);

ALTER TABLE ONLY conversion.integrations
    ADD CONSTRAINT integrations_report_credential_id_fkey FOREIGN KEY (report_credential_id) REFERENCES conversion.credentials(id);

ALTER TABLE ONLY conversion.legacy_outbox_imports
    ADD CONSTRAINT legacy_outbox_imports_event_id_fkey FOREIGN KEY (event_id) REFERENCES conversion.events(id);

ALTER TABLE ONLY conversion.meta_batches
    ADD CONSTRAINT meta_batches_credential_id_fkey FOREIGN KEY (credential_id) REFERENCES conversion.credentials(id);

ALTER TABLE ONLY conversion.meta_batches
    ADD CONSTRAINT meta_batches_destination_id_environment_fkey FOREIGN KEY (destination_id, environment) REFERENCES conversion.destinations(id, environment);

ALTER TABLE ONLY conversion.partner_report_attempts
    ADD CONSTRAINT partner_report_attempts_report_id_fkey FOREIGN KEY (report_id) REFERENCES conversion.partner_reports(id);

ALTER TABLE ONLY conversion.partner_reports
    ADD CONSTRAINT partner_reports_event_id_integration_id_fkey FOREIGN KEY (event_id, integration_id) REFERENCES conversion.events(id, integration_id);

ALTER TABLE ONLY conversion.partner_reports
    ADD CONSTRAINT partner_reports_original_delivery_id_event_id_delivery_rol_fkey FOREIGN KEY (original_delivery_id, event_id, delivery_role) REFERENCES conversion.deliveries(id, event_id, role);

ALTER TABLE ONLY conversion.payment_policy_counters
    ADD CONSTRAINT payment_policy_counters_integration_id_environment_fkey FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations(id, environment);

ALTER TABLE ONLY conversion.payment_policy_decisions
    ADD CONSTRAINT payment_policy_decisions_integration_id_environment_fkey FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations(id, environment);

ALTER TABLE ONLY conversion.payment_policy_decisions
    ADD CONSTRAINT payment_policy_decisions_original_destination_id_environme_fkey FOREIGN KEY (original_destination_id, environment) REFERENCES conversion.destinations(id, environment);

ALTER TABLE ONLY conversion.payment_policy_decisions
    ADD CONSTRAINT payment_policy_decisions_policy_scope_id_environment_integ_fkey FOREIGN KEY (policy_scope_id, environment, integration_id, app_scope) REFERENCES conversion.payment_policy_scopes(id, environment, integration_id, app_scope);

ALTER TABLE ONLY conversion.payment_policy_decisions
    ADD CONSTRAINT payment_policy_decisions_policy_scope_id_policy_version_id_fkey FOREIGN KEY (policy_scope_id, policy_version_id) REFERENCES conversion.payment_policy_versions(scope_id, id);

ALTER TABLE ONLY conversion.payment_policy_scopes
    ADD CONSTRAINT payment_policy_scopes_id_active_version_id_fkey FOREIGN KEY (id, active_version_id) REFERENCES conversion.payment_policy_versions(scope_id, id);

ALTER TABLE ONLY conversion.payment_policy_scopes
    ADD CONSTRAINT payment_policy_scopes_integration_id_environment_fkey FOREIGN KEY (integration_id, environment) REFERENCES conversion.integrations(id, environment);

ALTER TABLE ONLY conversion.payment_policy_versions
    ADD CONSTRAINT payment_policy_versions_scope_id_fkey FOREIGN KEY (scope_id) REFERENCES conversion.payment_policy_scopes(id);

ALTER TABLE ONLY public.admin_audit_logs
    ADD CONSTRAINT admin_audit_logs_admin_id_fkey FOREIGN KEY (admin_id) REFERENCES public.admins(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.admins
    ADD CONSTRAINT admins_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.roles(id);

ALTER TABLE ONLY public.bookshelves
    ADD CONSTRAINT bookshelves_novel_id_fkey FOREIGN KEY (novel_id) REFERENCES public.novels(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.bookshelves
    ADD CONSTRAINT bookshelves_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapter_content_object_fk FOREIGN KEY (content_object_key) REFERENCES public.chapter_content_objects(object_key);

ALTER TABLE ONLY public.chapter_entitlement_links
    ADD CONSTRAINT chapter_entitlement_links_chapter_id_novel_id_fkey FOREIGN KEY (chapter_id, novel_id) REFERENCES public.chapters(id, novel_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.chapter_entitlement_links
    ADD CONSTRAINT chapter_entitlement_links_source_chapter_id_novel_id_fkey FOREIGN KEY (source_chapter_id, novel_id) REFERENCES public.chapters(id, novel_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.chapters
    ADD CONSTRAINT chapters_novel_id_fkey FOREIGN KEY (novel_id) REFERENCES public.novels(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.feedback
    ADD CONSTRAINT feedback_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.gift_code_redemptions
    ADD CONSTRAINT gift_code_redemptions_gift_code_id_fkey FOREIGN KEY (gift_code_id) REFERENCES public.gift_codes(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.gift_code_redemptions
    ADD CONSTRAINT gift_code_redemptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.legacy_whole_book_proofs
    ADD CONSTRAINT legacy_whole_book_proofs_novel_id_fkey FOREIGN KEY (novel_id) REFERENCES public.novels(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.legacy_whole_book_proofs
    ADD CONSTRAINT legacy_whole_book_proofs_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.legacy_whole_book_proofs
    ADD CONSTRAINT legacy_whole_book_proofs_verified_by_fkey FOREIGN KEY (verified_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_aftercare_policies
    ADD CONSTRAINT payment_aftercare_policies_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_aftercare_policies
    ADD CONSTRAINT payment_aftercare_policies_finance_id_fkey FOREIGN KEY (finance_id) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_aftercare_policies
    ADD CONSTRAINT payment_aftercare_policies_reviewer_id_fkey FOREIGN KEY (reviewer_id) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_case_audit
    ADD CONSTRAINT payment_case_audit_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_case_audit
    ADD CONSTRAINT payment_case_audit_case_id_fkey FOREIGN KEY (case_id) REFERENCES public.payment_cases(id);

ALTER TABLE ONLY public.payment_case_evidence
    ADD CONSTRAINT payment_case_evidence_case_id_fkey FOREIGN KEY (case_id) REFERENCES public.payment_cases(id);

ALTER TABLE ONLY public.payment_case_reminders
    ADD CONSTRAINT payment_case_reminders_case_id_fkey FOREIGN KEY (case_id) REFERENCES public.payment_cases(id);

ALTER TABLE ONLY public.payment_cases
    ADD CONSTRAINT payment_cases_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_cases
    ADD CONSTRAINT payment_cases_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_channel_account_versions
    ADD CONSTRAINT payment_channel_account_versions_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_channel_account_versions
    ADD CONSTRAINT payment_channel_account_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_channel_account_versions
    ADD CONSTRAINT payment_channel_account_versions_published_by_fkey FOREIGN KEY (published_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_channel_accounts
    ADD CONSTRAINT payment_channel_accounts_published_by_fkey FOREIGN KEY (published_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_channel_credentials
    ADD CONSTRAINT payment_channel_credentials_account_id_version_fkey FOREIGN KEY (account_id, version) REFERENCES public.payment_channel_account_versions(account_id, version) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_channel_credentials
    ADD CONSTRAINT payment_channel_credentials_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_checkout_attempts
    ADD CONSTRAINT payment_checkout_attempts_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_checkout_attempts
    ADD CONSTRAINT payment_checkout_attempts_provider_account_id_fkey FOREIGN KEY (provider, account_id) REFERENCES public.payment_channel_accounts(provider, id);

ALTER TABLE ONLY public.payment_checkout_decisions
    ADD CONSTRAINT payment_checkout_decisions_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.payment_checkout_decisions
    ADD CONSTRAINT payment_checkout_decisions_provider_account_id_fkey FOREIGN KEY (provider, account_id) REFERENCES public.payment_channel_accounts(provider, id);

ALTER TABLE ONLY public.payment_checkout_decisions
    ADD CONSTRAINT payment_checkout_decisions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_checkout_requests
    ADD CONSTRAINT payment_checkout_requests_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_checkout_requests
    ADD CONSTRAINT payment_checkout_requests_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id);

ALTER TABLE ONLY public.payment_checkout_requests
    ADD CONSTRAINT payment_checkout_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_checkout_requests
    ADD CONSTRAINT payment_checkout_requests_vip_intent_id_fkey FOREIGN KEY (vip_intent_id) REFERENCES public.payment_vip_checkout_intents(intent_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_deferred_fulfillment
    ADD CONSTRAINT payment_deferred_fulfillment_case_id_fkey FOREIGN KEY (case_id) REFERENCES public.payment_cases(id);

ALTER TABLE ONLY public.payment_deferred_fulfillment
    ADD CONSTRAINT payment_deferred_fulfillment_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id);

ALTER TABLE ONLY public.payment_deferred_fulfillment
    ADD CONSTRAINT payment_deferred_fulfillment_receipt_id_fkey FOREIGN KEY (receipt_id) REFERENCES public.third_party_payment_orders(id);

ALTER TABLE ONLY public.payment_entitlement_adjustments
    ADD CONSTRAINT payment_entitlement_adjustments_freeze_case_id_fkey FOREIGN KEY (freeze_case_id) REFERENCES public.payment_cases(id);

ALTER TABLE ONLY public.payment_entitlement_adjustments
    ADD CONSTRAINT payment_entitlement_adjustments_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id);

ALTER TABLE ONLY public.payment_event_inbox
    ADD CONSTRAINT payment_event_account FOREIGN KEY (provider, account_id) REFERENCES public.payment_channel_accounts(provider, id);

ALTER TABLE ONLY public.payment_event_outbox
    ADD CONSTRAINT payment_event_outbox_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id);

ALTER TABLE ONLY public.payment_event_recovery
    ADD CONSTRAINT payment_event_recovery_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_event_recovery_failures
    ADD CONSTRAINT payment_event_recovery_failures_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_event_recovery_groups
    ADD CONSTRAINT payment_event_recovery_groups_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_fulfillment_funding
    ADD CONSTRAINT payment_fulfillment_funding_order_id_receipt_id_fkey FOREIGN KEY (order_id, receipt_id) REFERENCES public.third_party_payment_orders(order_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_ledger_amounts
    ADD CONSTRAINT payment_ledger_amounts_movement_id_evidence_id_fkey FOREIGN KEY (movement_id, evidence_id) REFERENCES public.payment_ledger_evidence(movement_id, id);

ALTER TABLE ONLY public.payment_ledger_amounts
    ADD CONSTRAINT payment_ledger_amounts_movement_id_fkey FOREIGN KEY (movement_id) REFERENCES public.payment_ledger_movements(id);

ALTER TABLE ONLY public.payment_ledger_evidence
    ADD CONSTRAINT payment_ledger_evidence_movement_id_fkey FOREIGN KEY (movement_id) REFERENCES public.payment_ledger_movements(id);

ALTER TABLE ONLY public.payment_ledger_movements
    ADD CONSTRAINT payment_ledger_movements_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_ledger_references
    ADD CONSTRAINT payment_ledger_references_movement_id_fkey FOREIGN KEY (movement_id) REFERENCES public.payment_ledger_movements(id);

ALTER TABLE ONLY public.payment_ledger_sync_settings
    ADD CONSTRAINT payment_ledger_sync_settings_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_ledger_sync_settings
    ADD CONSTRAINT payment_ledger_sync_settings_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_loss_commands
    ADD CONSTRAINT payment_loss_commands_case_id_fkey FOREIGN KEY (case_id) REFERENCES public.payment_cases(id);

ALTER TABLE ONLY public.payment_loss_commands
    ADD CONSTRAINT payment_loss_commands_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id);

ALTER TABLE ONLY public.payment_loss_commands
    ADD CONSTRAINT payment_loss_commands_requested_by_fkey FOREIGN KEY (requested_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_paypal_vip_plans
    ADD CONSTRAINT payment_paypal_vip_plans_provider_account_id_fkey FOREIGN KEY (provider, account_id) REFERENCES public.payment_channel_accounts(provider, id);

ALTER TABLE ONLY public.payment_paypal_vip_plans
    ADD CONSTRAINT payment_paypal_vip_plans_slot_id_fkey FOREIGN KEY (slot_id) REFERENCES public.payment_vip_offer_versions(slot_id);

ALTER TABLE ONLY public.third_party_payment_orders
    ADD CONSTRAINT payment_receipt_account FOREIGN KEY (payment_provider, account_id) REFERENCES public.payment_channel_accounts(provider, id);

ALTER TABLE ONLY public.payment_refund_commands
    ADD CONSTRAINT payment_refund_commands_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_refund_commands
    ADD CONSTRAINT payment_refund_commands_order_id_receipt_id_fkey FOREIGN KEY (order_id, receipt_id) REFERENCES public.third_party_payment_orders(order_id, id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_refund_failures
    ADD CONSTRAINT payment_refund_failures_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_refunds
    ADD CONSTRAINT payment_refunds_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_settlement_bank_reviews
    ADD CONSTRAINT payment_settlement_bank_reviews_entry_id_fkey FOREIGN KEY (entry_id) REFERENCES public.payment_bank_statement_entries(id);

ALTER TABLE ONLY public.payment_settlement_bank_reviews
    ADD CONSTRAINT payment_settlement_bank_reviews_payout_id_fkey FOREIGN KEY (payout_id) REFERENCES public.payment_settlement_payouts(id);

ALTER TABLE ONLY public.payment_settlement_commands
    ADD CONSTRAINT payment_settlement_commands_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_settlement_commands
    ADD CONSTRAINT payment_settlement_commands_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_settlement_imports
    ADD CONSTRAINT payment_settlement_imports_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_settlement_payout_evidence
    ADD CONSTRAINT payment_settlement_payout_evidence_import_id_fkey FOREIGN KEY (import_id) REFERENCES public.payment_settlement_imports(id);

ALTER TABLE ONLY public.payment_settlement_payout_evidence
    ADD CONSTRAINT payment_settlement_payout_evidence_payout_id_fkey FOREIGN KEY (payout_id) REFERENCES public.payment_settlement_payouts(id);

ALTER TABLE ONLY public.payment_settlement_payouts
    ADD CONSTRAINT payment_settlement_payouts_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_settlement_payouts
    ADD CONSTRAINT payment_settlement_payouts_account_id_reversal_key_fkey FOREIGN KEY (account_id, reversal_key) REFERENCES public.payment_ledger_movements(account_id, movement_key);

ALTER TABLE ONLY public.payment_settlement_payouts
    ADD CONSTRAINT payment_settlement_payouts_account_id_transfer_key_fkey FOREIGN KEY (account_id, transfer_key) REFERENCES public.payment_ledger_movements(account_id, movement_key);

ALTER TABLE ONLY public.payment_settlement_rows
    ADD CONSTRAINT payment_settlement_rows_import_id_fkey FOREIGN KEY (import_id) REFERENCES public.payment_settlement_imports(id);

ALTER TABLE ONLY public.payment_settlement_rows
    ADD CONSTRAINT payment_settlement_rows_movement_id_fkey FOREIGN KEY (movement_id) REFERENCES public.payment_ledger_movements(id);

ALTER TABLE ONLY public.payment_stripe_checkout_terms
    ADD CONSTRAINT payment_stripe_checkout_terms_attempt_id_fkey FOREIGN KEY (attempt_id) REFERENCES public.payment_checkout_attempts(id);

ALTER TABLE ONLY public.payment_stripe_customers
    ADD CONSTRAINT payment_stripe_customers_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_stripe_customers
    ADD CONSTRAINT payment_stripe_customers_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.payment_stripe_invoice_allocations
    ADD CONSTRAINT payment_stripe_invoice_allocations_account_id_invoice_ref_fkey FOREIGN KEY (account_id, invoice_ref) REFERENCES public.payment_stripe_invoice_periods(account_id, invoice_ref);

ALTER TABLE ONLY public.payment_stripe_invoice_allocations
    ADD CONSTRAINT payment_stripe_invoice_allocations_receipt_id_fkey FOREIGN KEY (receipt_id) REFERENCES public.third_party_payment_orders(id);

ALTER TABLE ONLY public.payment_stripe_invoice_periods
    ADD CONSTRAINT payment_stripe_invoice_periods_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_stripe_invoice_periods
    ADD CONSTRAINT payment_stripe_invoice_periods_intent_id_fkey FOREIGN KEY (intent_id) REFERENCES public.payment_stripe_subscription_intents(id);

ALTER TABLE ONLY public.payment_stripe_invoice_periods
    ADD CONSTRAINT payment_stripe_invoice_periods_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id);

ALTER TABLE ONLY public.payment_stripe_market_reviews
    ADD CONSTRAINT payment_stripe_market_reviews_approved_by_fkey FOREIGN KEY (approved_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_stripe_market_reviews
    ADD CONSTRAINT payment_stripe_market_reviews_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id);

ALTER TABLE ONLY public.payment_stripe_one_time_offers
    ADD CONSTRAINT payment_stripe_one_time_offers_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_stripe_one_time_offers
    ADD CONSTRAINT payment_stripe_one_time_offers_slot_id_fkey FOREIGN KEY (slot_id) REFERENCES public.recharge_slots(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payment_stripe_refund_items
    ADD CONSTRAINT payment_stripe_refund_items_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_stripe_refund_items
    ADD CONSTRAINT payment_stripe_refund_items_receipt_id_fkey FOREIGN KEY (receipt_id) REFERENCES public.third_party_payment_orders(id);

ALTER TABLE ONLY public.payment_stripe_report_runs
    ADD CONSTRAINT payment_stripe_report_runs_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_stripe_report_runs
    ADD CONSTRAINT payment_stripe_report_runs_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_stripe_report_runs
    ADD CONSTRAINT payment_stripe_report_runs_import_command_id_fkey FOREIGN KEY (import_command_id) REFERENCES public.payment_settlement_commands(id);

ALTER TABLE ONLY public.payment_stripe_review_approvals
    ADD CONSTRAINT payment_stripe_review_approvals_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_stripe_review_approvals
    ADD CONSTRAINT payment_stripe_review_approvals_case_id_fkey FOREIGN KEY (case_id) REFERENCES public.payment_cases(id);

ALTER TABLE ONLY public.payment_stripe_review_approvals
    ADD CONSTRAINT payment_stripe_review_approvals_requested_by_fkey FOREIGN KEY (requested_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.payment_stripe_subscription_intents
    ADD CONSTRAINT payment_stripe_subscription_intents_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_stripe_subscription_intents
    ADD CONSTRAINT payment_stripe_subscription_intents_offer_version_id_fkey FOREIGN KEY (offer_version_id) REFERENCES public.payment_vip_offer_versions(slot_id);

ALTER TABLE ONLY public.payment_stripe_subscription_intents
    ADD CONSTRAINT payment_stripe_subscription_intents_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id);

ALTER TABLE ONLY public.payment_stripe_subscription_intents
    ADD CONSTRAINT payment_stripe_subscription_intents_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);

ALTER TABLE ONLY public.payment_stripe_vip_offers
    ADD CONSTRAINT payment_stripe_vip_offers_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.payment_stripe_vip_offers
    ADD CONSTRAINT payment_stripe_vip_offers_offer_version_id_fkey FOREIGN KEY (offer_version_id) REFERENCES public.payment_vip_offer_versions(slot_id);

ALTER TABLE ONLY public.payment_vip_checkout_intents
    ADD CONSTRAINT payment_vip_checkout_intents_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id);

ALTER TABLE ONLY public.payment_vip_checkout_intents
    ADD CONSTRAINT payment_vip_checkout_intents_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.paypal_subscription_billing_snapshots
    ADD CONSTRAINT paypal_subscription_billing_snapshots_subscription_id_fkey FOREIGN KEY (subscription_id) REFERENCES public.user_subscriptions(subscription_id) ON DELETE CASCADE;

ALTER TABLE ONLY public.paypal_subscription_intents
    ADD CONSTRAINT paypal_subscription_intents_checkout_account_id_fkey FOREIGN KEY (checkout_account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.paypal_subscription_intents
    ADD CONSTRAINT paypal_subscription_intents_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id);

ALTER TABLE ONLY public.paypal_subscription_intents
    ADD CONSTRAINT paypal_subscription_intents_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.paypal_subscription_intents
    ADD CONSTRAINT paypal_subscription_intents_vip_offer_version_id_fkey FOREIGN KEY (vip_offer_version_id) REFERENCES public.payment_vip_offer_versions(slot_id);

ALTER TABLE ONLY public.product_channel_bindings
    ADD CONSTRAINT product_channel_bindings_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.payment_channel_accounts(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.product_channel_bindings
    ADD CONSTRAINT product_channel_bindings_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.product_channel_bindings
    ADD CONSTRAINT product_channel_bindings_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.product_channel_bindings
    ADD CONSTRAINT product_channel_bindings_provider_account_id_fkey FOREIGN KEY (provider, account_id) REFERENCES public.payment_channel_accounts(provider, id);

ALTER TABLE ONLY public.product_channel_bindings
    ADD CONSTRAINT product_channel_bindings_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.product_publications
    ADD CONSTRAINT product_publications_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.product_publications
    ADD CONSTRAINT product_publications_published_by_fkey FOREIGN KEY (published_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.product_publications
    ADD CONSTRAINT product_publications_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.products
    ADD CONSTRAINT products_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.admins(id);

ALTER TABLE ONLY public.products
    ADD CONSTRAINT products_source_product_id_fkey FOREIGN KEY (source_product_id) REFERENCES public.products(id);

ALTER TABLE ONLY public.promotion_links
    ADD CONSTRAINT promotion_links_domain_id_fkey FOREIGN KEY (domain_id) REFERENCES public.system_domains(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.promotion_links
    ADD CONSTRAINT promotion_links_fb_pixel_id_fkey FOREIGN KEY (fb_pixel_id) REFERENCES public.fb_pixels(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.promotion_links
    ADD CONSTRAINT promotion_links_novel_id_fkey FOREIGN KEY (novel_id) REFERENCES public.novels(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.promotion_links
    ADD CONSTRAINT promotion_links_recharge_template_id_fkey FOREIGN KEY (recharge_template_id) REFERENCES public.recharge_templates(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.reader_payment_promotion_coverage
    ADD CONSTRAINT reader_payment_promotion_coverage_promotion_link_id_fkey FOREIGN KEY (promotion_link_id) REFERENCES public.promotion_links(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.reader_sessions
    ADD CONSTRAINT reader_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.recharge_orders
    ADD CONSTRAINT recharge_orders_novel_id_fkey FOREIGN KEY (novel_id) REFERENCES public.novels(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.recharge_orders
    ADD CONSTRAINT recharge_orders_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id);

ALTER TABLE ONLY public.recharge_orders
    ADD CONSTRAINT recharge_orders_promotion_link_id_fkey FOREIGN KEY (promotion_link_id) REFERENCES public.promotion_links(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.recharge_orders
    ADD CONSTRAINT recharge_orders_recharge_template_id_fkey FOREIGN KEY (recharge_template_id) REFERENCES public.recharge_templates(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.recharge_orders
    ADD CONSTRAINT recharge_orders_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.recharge_slots
    ADD CONSTRAINT recharge_slots_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id) ON DELETE RESTRICT;

ALTER TABLE ONLY public.recharge_slots
    ADD CONSTRAINT recharge_slots_template_id_fkey FOREIGN KEY (template_id) REFERENCES public.recharge_templates(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.recharge_slots
    ADD CONSTRAINT recharge_slots_vip_offer_version_id_fkey FOREIGN KEY (vip_offer_version_id) REFERENCES public.payment_vip_offer_versions(slot_id);

ALTER TABLE ONLY public.recommendations
    ADD CONSTRAINT recommendations_novel_id_fkey FOREIGN KEY (novel_id) REFERENCES public.novels(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.role_permissions
    ADD CONSTRAINT role_permissions_role_id_fkey FOREIGN KEY (role_id) REFERENCES public.roles(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.third_party_payment_orders
    ADD CONSTRAINT third_party_payment_orders_order_id_fkey FOREIGN KEY (order_id) REFERENCES public.recharge_orders(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.transactions
    ADD CONSTRAINT transactions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.unlock_records
    ADD CONSTRAINT unlock_chapter_identity FOREIGN KEY (chapter_id, novel_id) REFERENCES public.chapters(id, novel_id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE ONLY public.unlock_records
    ADD CONSTRAINT unlock_records_novel_id_fkey FOREIGN KEY (novel_id) REFERENCES public.novels(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.unlock_records
    ADD CONSTRAINT unlock_records_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_capi_subscribe_order_id_fkey FOREIGN KEY (capi_subscribe_order_id) REFERENCES public.recharge_orders(id);

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_checkout_account_id_fkey FOREIGN KEY (checkout_account_id) REFERENCES public.payment_channel_accounts(id);

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_product_id_fkey FOREIGN KEY (product_id) REFERENCES public.products(id);

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.user_subscriptions
    ADD CONSTRAINT user_subscriptions_vip_offer_version_id_fkey FOREIGN KEY (vip_offer_version_id) REFERENCES public.payment_vip_offer_versions(slot_id);

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_promotion_link_id_fkey FOREIGN KEY (promotion_link_id) REFERENCES public.promotion_links(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.wallets
    ADD CONSTRAINT wallets_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;

REVOKE ALL ON FUNCTION conversion.guard_promotion_pixel() FROM PUBLIC;

REVOKE ALL ON FUNCTION conversion.lock_active_admin(operator_id text) FROM PUBLIC;

REVOKE ALL ON FUNCTION conversion.lock_active_admin_for_permission(operator_id text, required_permission text) FROM PUBLIC;

REVOKE ALL ON FUNCTION conversion.lock_active_super_admin(operator_id text) FROM PUBLIC;

REVOKE ALL ON FUNCTION conversion.lock_repair_order(order_id bigint) FROM PUBLIC;

REVOKE ALL ON FUNCTION conversion.reader_order_environment(target_order bigint, frozen text, checkout text) FROM PUBLIC;

REVOKE ALL ON FUNCTION conversion.reader_payment_attribution_time(target_order bigint, pixel text) FROM PUBLIC;

REVOKE ALL ON FUNCTION conversion.sinan_route_issue(event_key bigint, destination_key bigint) FROM PUBLIC;

REVOKE ALL ON FUNCTION public.discard_unfulfilled_legacy_conversion() FROM PUBLIC;

REVOKE ALL ON FUNCTION public.reader_payment_coverage_verified(event_environment text, link integer) FROM PUBLIC;

REVOKE ALL ON FUNCTION public.reader_verified_first_payment(event_environment text, target_order bigint, target_user bigint, target_promotion integer) FROM PUBLIC;

REVOKE ALL ON FUNCTION public.reader_verified_subscription_payment_kind(event_environment text, target_order bigint, target_user bigint) FROM PUBLIC;

-- Query indexes
CREATE INDEX IF NOT EXISTS idx_orders_subscription_period
    ON public.recharge_orders(subscription_id, paid_period_end) WHERE paid_period_end IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_whole_book_entitlement_v2
    ON public.recharge_orders(user_id, novel_id)
    WHERE order_type = 'whole_book' AND status IN ('Success','Paid','RefundPending') AND fulfilled_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_transactions_user_feed
    ON public.transactions(user_id, (COALESCE(created_at, TIMESTAMPTZ '0001-01-01 00:00:00+00')) DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_novels_reader_popular
    ON public.novels(view_count DESC, created_at DESC, id DESC) WHERE status <> 'archived' AND publish_status = 'published';

CREATE INDEX IF NOT EXISTS idx_novels_reader_latest
    ON public.novels(created_at DESC, id DESC) WHERE status <> 'archived' AND publish_status = 'published';

CREATE INDEX IF NOT EXISTS idx_payment_outbox_pending
    ON public.payment_event_outbox(next_attempt_at, event_id) WHERE state = 'pending' AND delivered_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_reader_sessions_user ON public.reader_sessions(user_id);

CREATE INDEX IF NOT EXISTS idx_paypal_order_recent_pending
    ON public.recharge_orders(created_at, (COALESCE(reconciliation_at, created_at)))
    WHERE payment_method = 'paypal' AND external_ref_id IS NOT NULL AND external_ref_id NOT LIKE 'mock%'
      AND status = 'Pending' AND checkout_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_paypal_order_retained_claim
    ON public.recharge_orders((COALESCE(reconciliation_at, created_at)), id)
    WHERE payment_method = 'paypal' AND external_ref_id IS NOT NULL AND external_ref_id NOT LIKE 'mock%'
      AND ((status = 'Pending' AND checkout_id IS NULL)
        OR (status IN ('Success','Paid') AND order_type = 'whole_book' AND fulfilled_at IS NULL));

CREATE INDEX IF NOT EXISTS idx_paypal_cancel_claim
    ON public.user_subscriptions((COALESCE(cancel_next_attempt_at, updated_at)), id)
    WHERE status = 'CANCEL_PENDING' AND payment_method = 'paypal';

CREATE INDEX IF NOT EXISTS idx_paypal_subscription_continuous
    ON public.user_subscriptions((COALESCE(paypal_next_check_at, created_at)), id)
    WHERE payment_method = 'paypal' AND subscription_id NOT LIKE 'I-MOCK%'
      AND (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR paypal_recovery_requested);

CREATE INDEX IF NOT EXISTS idx_paypal_subscription_recent
    ON public.user_subscriptions(paypal_status_changed_at, (COALESCE(paypal_next_check_at, created_at)))
    WHERE payment_method = 'paypal' AND subscription_id NOT LIKE 'I-MOCK%'
      AND NOT (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR paypal_recovery_requested);

CREATE INDEX IF NOT EXISTS idx_outbox_unattributed_oldest
    ON public.payment_event_outbox(created_at) WHERE state = 'unattributed';

CREATE INDEX IF NOT EXISTS idx_payment_order_attention
    ON public.recharge_orders(id)
    WHERE (status IN ('Success','Paid') AND fulfilled_at IS NULL)
       OR (status = 'Pending' AND payment_method = 'paypal')
       OR (reconciliation_note IS NOT NULL AND status <> 'Refunded');

CREATE INDEX IF NOT EXISTS idx_reader_shelf_activity
    ON public.bookshelves(user_id, (COALESCE(GREATEST(updated_at, shelf_added_at), TIMESTAMPTZ 'epoch')) DESC, novel_id DESC)
    WHERE in_shelf = true;

CREATE INDEX IF NOT EXISTS idx_promotion_links_utm ON public.promotion_links(utm_source, utm_campaign);


CREATE INDEX IF NOT EXISTS idx_reader_paid_vip_expiry
    ON public.user_subscriptions(user_id, current_period_end) INCLUDE (subscription_id)
    WHERE status IN ('ACTIVE','CANCELLED','CANCEL_PENDING')
      AND last_payment_time IS NOT NULL AND subscription_id NOT LIKE 'I-MOCK%';

CREATE INDEX IF NOT EXISTS recharge_orders_conversion_fulfilled_at
    ON public.recharge_orders (fulfilled_at, id) WHERE fulfilled_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS conversion_outbox_retention
    ON public.reader_conversion_outbox (environment, consumed_at, business_key)
    WHERE consumed_at IS NOT NULL AND snapshot <> '{}';
CREATE INDEX IF NOT EXISTS conversion_repair_fact_retention
    ON public.reader_conversion_repair_facts (environment, created_at, business_key)
    WHERE snapshot <> '{}';
CREATE INDEX IF NOT EXISTS conversion_event_retention
    ON conversion.events (environment, created_at, id)
    WHERE source_url IS NOT NULL OR matching_data <> '{}';
CREATE INDEX IF NOT EXISTS conversion_inbox_retention
    ON conversion.inbox (received_at, id)
    WHERE payload IS NOT NULL AND state IN ('consumed','quarantined');

CREATE INDEX IF NOT EXISTS conversion_events_rule_fact_missing
    ON conversion.events (environment, received_at)
    WHERE reason = 'rule_fact_missing';

CREATE INDEX IF NOT EXISTS conversion_audit_policy_publication
    ON conversion.operation_audit (environment, subject_id, operator_id, (after_state->>'publication_id'))
    WHERE action IN ('publish_payment_policy','inherit_payment_policy') AND after_state ? 'publication_id';

-- Stage metrics use independent time windows for receipt, normalization and confirmation.
CREATE INDEX IF NOT EXISTS conversion_outbox_throughput_created
    ON public.reader_conversion_outbox (environment, created_at);
CREATE INDEX IF NOT EXISTS conversion_outbox_throughput_consumed
    ON public.reader_conversion_outbox (environment, consumed_at) WHERE consumed_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS conversion_inbox_throughput_received
    ON conversion.inbox (integration_id, received_at);
CREATE INDEX IF NOT EXISTS conversion_deliveries_throughput_created
    ON conversion.deliveries (environment, created_at);
CREATE INDEX IF NOT EXISTS conversion_deliveries_throughput_finalized
    ON conversion.deliveries (environment, finalized_at) WHERE finalized_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS conversion_attempts_throughput_sending
    ON conversion.delivery_attempts (sending_at) WHERE sending_at IS NOT NULL;
