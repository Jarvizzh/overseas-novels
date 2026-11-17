-- Use a stable state transition time for scheduling. Polling and metadata
-- updates may still change updated_at for other consumers.
ALTER TABLE user_subscriptions ADD COLUMN paypal_status_changed_at TIMESTAMPTZ;

UPDATE user_subscriptions SET paypal_status_changed_at = CASE status
    WHEN 'CANCELLED' THEN COALESCE(cancelled_at, updated_at, created_at, now())
    WHEN 'EXPIRED' THEN LEAST(COALESCE(LEAST(current_period_end, updated_at), created_at, now()), now())
    ELSE COALESCE(updated_at, created_at, now())
END;

ALTER TABLE user_subscriptions
    ALTER COLUMN paypal_status_changed_at SET DEFAULT clock_timestamp(),
    ALTER COLUMN paypal_status_changed_at SET NOT NULL;

CREATE FUNCTION track_paypal_subscription_status_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        NEW.paypal_status_changed_at := clock_timestamp();
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER paypal_subscription_status_change BEFORE UPDATE OF status ON user_subscriptions
FOR EACH ROW EXECUTE FUNCTION track_paypal_subscription_status_change();

-- Keep exact counts and attempt maxima without counting the event history on
-- each sample. Shard by order so the two events of one order share a shard.
CREATE TABLE payment_unattributed_counts (
    shard SMALLINT NOT NULL,
    attempts INTEGER NOT NULL,
    event_count BIGINT NOT NULL CHECK (event_count >= 0),
    PRIMARY KEY (shard, attempts)
);

-- Block concurrent writes between the snapshot and trigger installation.
LOCK TABLE payment_event_outbox IN SHARE ROW EXCLUSIVE MODE;
INSERT INTO payment_unattributed_counts (shard, attempts, event_count)
SELECT (order_id % 32)::smallint, attempts, count(*)
FROM payment_event_outbox WHERE state = 'unattributed'
GROUP BY 1, 2;

CREATE FUNCTION track_payment_unattributed_count() RETURNS trigger
LANGUAGE plpgsql AS $$
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

CREATE TRIGGER payment_unattributed_count
AFTER INSERT OR DELETE OR UPDATE OF state, order_id, attempts ON payment_event_outbox
FOR EACH ROW EXECUTE FUNCTION track_payment_unattributed_count();

CREATE TABLE payment_task_metrics_schedule (
    singleton BOOLEAN PRIMARY KEY CHECK (singleton),
    next_collection_at TIMESTAMPTZ NOT NULL
);

INSERT INTO reader_schema_migrations (version) VALUES (2026111704);
