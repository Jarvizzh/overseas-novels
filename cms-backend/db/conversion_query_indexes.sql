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

CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_events_rule_fact_missing
    ON conversion.events (environment, received_at)
    WHERE reason = 'rule_fact_missing';

CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_audit_policy_publication
    ON conversion.operation_audit (environment, subject_id, operator_id, (after_state->>'publication_id'))
    WHERE action IN ('publish_payment_policy','inherit_payment_policy') AND after_state ? 'publication_id';

-- Stage metrics use independent time windows for receipt, normalization and confirmation.
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_outbox_throughput_created
    ON reader_conversion_outbox (environment, created_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_outbox_throughput_consumed
    ON reader_conversion_outbox (environment, consumed_at) WHERE consumed_at IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_inbox_throughput_received
    ON conversion.inbox (integration_id, received_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_deliveries_throughput_created
    ON conversion.deliveries (environment, created_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_deliveries_throughput_finalized
    ON conversion.deliveries (environment, finalized_at) WHERE finalized_at IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS conversion_attempts_throughput_sending
    ON conversion.delivery_attempts (sending_at) WHERE sending_at IS NOT NULL;
