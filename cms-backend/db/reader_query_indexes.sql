-- Run with psql -X -v ON_ERROR_STOP=1 -f reader_query_indexes.sql.
-- Run outside a transaction and serialize executions for the same database.
-- Failed concurrent builds can leave invalid indexes. Stop instead of silently
-- accepting them through CREATE INDEX IF NOT EXISTS.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM reader_schema_migrations WHERE version = 2026111704) THEN
        RAISE EXCEPTION 'Apply Reader schema migration 2026111704 before building query indexes';
    END IF;
    IF EXISTS (
        SELECT FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
        WHERE c.relnamespace = current_schema()::regnamespace
          AND NOT i.indisvalid
          AND c.relname = ANY(ARRAY[
              'idx_orders_subscription_period', 'idx_whole_book_entitlement_v2',
              'idx_transactions_user_feed', 'idx_novels_reader_popular', 'idx_novels_reader_latest',
              'idx_payment_outbox_pending', 'idx_reader_sessions_user',
              'idx_paypal_order_recent_pending', 'idx_paypal_order_retained_claim',
              'idx_paypal_cancel_claim', 'idx_payment_order_attention',
              'idx_promotion_links_utm', 'idx_reader_paid_vip_expiry',
              'idx_paypal_subscription_continuous', 'idx_paypal_subscription_recent',
              'idx_outbox_unattributed_oldest', 'idx_reader_shelf_activity'
          ])
    ) THEN
        RAISE EXCEPTION 'Invalid Reader query index exists. Inspect pg_index before retrying';
    END IF;
END $$;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_orders_subscription_period
    ON recharge_orders(subscription_id, paid_period_end) WHERE paid_period_end IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_whole_book_entitlement_v2
    ON recharge_orders(user_id, novel_id)
    WHERE order_type = 'whole_book' AND status IN ('Success','Paid','RefundPending') AND fulfilled_at IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_transactions_user_feed
    ON transactions(user_id, (COALESCE(created_at, TIMESTAMPTZ '0001-01-01 00:00:00+00')) DESC, id DESC);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_novels_reader_popular
    ON novels(view_count DESC, created_at DESC, id DESC) WHERE status <> 'archived' AND publish_status = 'published';

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_novels_reader_latest
    ON novels(created_at DESC, id DESC) WHERE status <> 'archived' AND publish_status = 'published';

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_payment_outbox_pending
    ON payment_event_outbox(next_attempt_at, event_id) WHERE state = 'pending' AND delivered_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_reader_sessions_user ON reader_sessions(user_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_paypal_order_recent_pending
    ON recharge_orders(created_at, (COALESCE(reconciliation_at, created_at)))
    WHERE payment_method = 'paypal' AND external_ref_id IS NOT NULL AND external_ref_id NOT LIKE 'mock%'
      AND status = 'Pending' AND checkout_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_paypal_order_retained_claim
    ON recharge_orders((COALESCE(reconciliation_at, created_at)), id)
    WHERE payment_method = 'paypal' AND external_ref_id IS NOT NULL AND external_ref_id NOT LIKE 'mock%'
      AND ((status = 'Pending' AND checkout_id IS NULL)
        OR (status IN ('Success','Paid') AND order_type = 'whole_book' AND fulfilled_at IS NULL));

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_paypal_cancel_claim
    ON user_subscriptions((COALESCE(cancel_next_attempt_at, updated_at)), id)
    WHERE status = 'CANCEL_PENDING' AND payment_method = 'paypal';

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_paypal_subscription_continuous
    ON user_subscriptions((COALESCE(paypal_next_check_at, created_at)), id)
    WHERE payment_method = 'paypal' AND subscription_id NOT LIKE 'I-MOCK%'
      AND (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR paypal_recovery_requested);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_paypal_subscription_recent
    ON user_subscriptions(paypal_status_changed_at, (COALESCE(paypal_next_check_at, created_at)))
    WHERE payment_method = 'paypal' AND subscription_id NOT LIKE 'I-MOCK%'
      AND NOT (status IN ('ACTIVE','SUSPENDED','CANCEL_PENDING') OR paypal_recovery_requested);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_unattributed_oldest
    ON payment_event_outbox(created_at) WHERE state = 'unattributed';

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_payment_order_attention
    ON recharge_orders(id)
    WHERE (status IN ('Success','Paid') AND fulfilled_at IS NULL)
       OR (status = 'Pending' AND payment_method = 'paypal')
       OR (reconciliation_note IS NOT NULL AND status <> 'Refunded');

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_reader_shelf_activity
    ON bookshelves(user_id, (COALESCE(GREATEST(updated_at, shelf_added_at), TIMESTAMPTZ 'epoch')) DESC, novel_id DESC)
    WHERE in_shelf = true;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_promotion_links_utm ON promotion_links(utm_source, utm_campaign);


CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_reader_paid_vip_expiry
    ON user_subscriptions(user_id, current_period_end) INCLUDE (subscription_id)
    WHERE status IN ('ACTIVE','CANCELLED','CANCEL_PENDING')
      AND last_payment_time IS NOT NULL AND subscription_id NOT LIKE 'I-MOCK%';
