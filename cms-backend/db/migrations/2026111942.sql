-- Allow editing and deleting never-published, unbound, unreferenced draft products
-- while preserving strict immutability once a product is bound to a channel, published,
-- referenced by a template/order/subscription/checkout intent, or copied into a child version.

CREATE OR REPLACE FUNCTION is_product_mutable_draft(p_product_id BIGINT) RETURNS BOOLEAN
LANGUAGE sql STABLE AS $$
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

CREATE OR REPLACE FUNCTION forbid_product_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
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

DROP TRIGGER IF EXISTS trg_products_immutable ON products;
CREATE TRIGGER trg_products_immutable
    BEFORE UPDATE OR DELETE ON products
    FOR EACH ROW EXECUTE FUNCTION forbid_product_mutation();

CREATE OR REPLACE FUNCTION forbid_non_draft_publication_delete() RETURNS trigger LANGUAGE plpgsql AS $$
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

DROP TRIGGER IF EXISTS trg_product_publications_protect_non_draft ON product_publications;
CREATE TRIGGER trg_product_publications_protect_non_draft
    BEFORE DELETE ON product_publications
    FOR EACH ROW EXECUTE FUNCTION forbid_non_draft_publication_delete();

CREATE OR REPLACE FUNCTION forbid_active_product_binding_delete() RETURNS trigger LANGUAGE plpgsql AS $$
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

DROP TRIGGER IF EXISTS trg_product_bindings_protect_active ON product_channel_bindings;
CREATE TRIGGER trg_product_bindings_protect_active
    BEFORE DELETE ON product_channel_bindings
    FOR EACH ROW EXECUTE FUNCTION forbid_active_product_binding_delete();

INSERT INTO reader_schema_migrations(version) VALUES (2026111942)
ON CONFLICT (version) DO NOTHING;
