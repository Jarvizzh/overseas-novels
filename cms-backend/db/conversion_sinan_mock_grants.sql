\set ON_ERROR_STOP on
-- Run only in the isolated test database, with two dedicated login roles.
BEGIN;
GRANT USAGE ON SCHEMA conversion TO :"callback_role", :"partner_worker_role";
GRANT SELECT ON reader_schema_migrations TO :"callback_role", :"partner_worker_role";

GRANT SELECT (id,source,account_key,environment,enabled,allowed_apps,allowed_events,contract_version,
    inbound_credential_id,report_config) ON conversion.integrations TO :"callback_role";
GRANT UPDATE (id) ON conversion.integrations TO :"callback_role";
GRANT SELECT (id,key_version,ciphertext) ON conversion.credentials TO :"callback_role";
GRANT SELECT (integration_id,destination_id,environment) ON conversion.integration_destinations TO :"callback_role";
GRANT SELECT (id,pixel_id,media) ON conversion.destinations TO :"callback_role";
GRANT INSERT (integration_id,source_event_type,source_event_id,source_order_id,received_at,payload,payload_digest,state,reason)
    ON conversion.inbox TO :"callback_role";
GRANT SELECT (integration_id,source_event_type,source_event_id,state,payload_digest)
    ON conversion.inbox TO :"callback_role";

GRANT SELECT (id,environment,source,enabled,contract_version,report_config,report_credential_id)
    ON conversion.integrations TO :"partner_worker_role";
GRANT SELECT (id,key_version,ciphertext) ON conversion.credentials TO :"partner_worker_role";
GRANT SELECT ON conversion.partner_reports,conversion.partner_report_attempts,conversion.worker_heartbeats
    TO :"partner_worker_role";
GRANT UPDATE (state,reason,next_attempt_at,attempts,lease_token,lease_until,wire_payload)
    ON conversion.partner_reports TO :"partner_worker_role";
GRANT INSERT (report_id,attempt_no), UPDATE (sending_at,recorded_at,outcome,reason,retry_at,replay_allowed)
    ON conversion.partner_report_attempts TO :"partner_worker_role";
GRANT INSERT, UPDATE ON conversion.worker_heartbeats TO :"partner_worker_role";
COMMIT;
