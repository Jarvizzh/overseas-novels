-- Run outside a transaction after applying migration 2026111909.
CREATE INDEX CONCURRENTLY IF NOT EXISTS recharge_orders_conversion_fulfilled_at
    ON recharge_orders (fulfilled_at, id) WHERE fulfilled_at IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_outbox_retention
    ON reader_conversion_outbox (environment, consumed_at, business_key)
    WHERE consumed_at IS NOT NULL AND snapshot <> '{}';
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_repair_fact_retention
    ON reader_conversion_repair_facts (environment, created_at, business_key)
    WHERE snapshot <> '{}';
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_event_retention
    ON conversion.events (environment, created_at, id)
    WHERE source_url IS NOT NULL OR matching_data <> '{}';
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_inbox_retention
    ON conversion.inbox (received_at, id)
    WHERE payload IS NOT NULL AND state IN ('consumed','quarantined');
