\set ON_ERROR_STOP on
-- Run as migration owner. Each variable names a distinct, pre-provisioned
-- non-owner login. Existing Reader and CMS business-schema privileges remain.
BEGIN;

GRANT USAGE ON SCHEMA conversion TO :"cms_role", :"worker_role", :"api_role", :"collector_role", :"retention_role";
GRANT SELECT ON reader_schema_migrations TO
    :"reader_role", :"cms_role", :"worker_role", :"api_role", :"collector_role", :"retention_role";

-- The compatibility table supplies only Pixel identities to runtime clients.
REVOKE SELECT,INSERT,UPDATE,DELETE ON fb_pixels FROM :"reader_role", :"cms_role";
REVOKE SELECT (access_token),INSERT (access_token),UPDATE (access_token)
    ON fb_pixels FROM :"reader_role", :"cms_role";
GRANT SELECT (id,name,pixel_id,created_at) ON fb_pixels TO :"reader_role", :"cms_role";

-- Reader writes producer facts. It cannot read conversion schema or modify
-- proof of a prior send.
GRANT INSERT (business_key,event_name,order_id,intent_id,environment,occurred_at,created_at,snapshot)
    ON reader_conversion_outbox TO :"reader_role";
GRANT SELECT (business_key,environment,created_at,consumed_at) ON reader_conversion_outbox TO :"reader_role";
GRANT INSERT (business_key,event_name,order_id,environment,occurred_at,created_at,snapshot)
    ON reader_conversion_repair_facts TO :"reader_role";
GRANT SELECT (business_key) ON reader_conversion_repair_facts TO :"reader_role";
GRANT SELECT (created_at,state) ON payment_event_outbox TO :"reader_role";
GRANT SELECT ON payment_unattributed_counts TO :"reader_role";

-- CMS serves legacy Pixel selection metadata; operational actions go through
-- the private API. Neither CMS nor the API reads credential ciphertext.
GRANT SELECT (id,legacy_fb_pixel_id,display_name,enabled,retired_at) ON conversion.destinations TO :"cms_role";
GRANT SELECT (destination_id,integration_id) ON conversion.integration_destinations TO :"cms_role";
GRANT SELECT (id,source,account_key) ON conversion.integrations TO :"cms_role";

GRANT SELECT ON reader_conversion_outbox,reader_conversion_repair_facts TO :"worker_role";
GRANT UPDATE (consumed_at,processing_error) ON reader_conversion_outbox TO :"worker_role";
GRANT UPDATE (normalized_at,original_send_started_at) ON reader_conversion_repair_facts TO :"worker_role";
GRANT SELECT (status,domain) ON system_domains TO :"worker_role";
GRANT SELECT (id,fb_pixel_id) ON promotion_links TO :"worker_role";
GRANT SELECT (id,pixel_id) ON fb_pixels TO :"worker_role";
GRANT EXECUTE ON FUNCTION conversion.sinan_route_issue(bigint,bigint) TO :"worker_role", :"api_role";
GRANT SELECT ON conversion.inbox TO :"worker_role";
GRANT UPDATE (state,reason) ON conversion.inbox TO :"worker_role";
GRANT UPDATE (id) ON conversion.integrations TO :"worker_role";
GRANT SELECT ON conversion.credentials,conversion.integrations,conversion.integration_destinations,
    conversion.destinations,conversion.events,conversion.deliveries,conversion.delivery_attempts,
    conversion.delivery_diagnostics,conversion.partner_reports,conversion.worker_heartbeats TO :"worker_role";
GRANT INSERT ON conversion.events,conversion.deliveries,conversion.delivery_attempts,
    conversion.delivery_diagnostics,conversion.partner_reports,conversion.worker_heartbeats TO :"worker_role";
GRANT UPDATE (next_claim_at) ON conversion.destinations TO :"worker_role";
GRANT UPDATE ON conversion.deliveries,conversion.delivery_attempts,conversion.worker_heartbeats TO :"worker_role";

GRANT EXECUTE ON FUNCTION conversion.lock_active_admin(text),conversion.lock_repair_order(bigint) TO :"api_role";
GRANT SELECT (id,status,order_type,subscription_id,fulfilled_at,paid_at,capi_expectation,reconciliation_note)
    ON recharge_orders TO :"api_role";
GRANT SELECT (subscription_id,capi_subscribe_state,capi_subscribe_order_id) ON user_subscriptions TO :"api_role";
GRANT SELECT (status,domain) ON system_domains TO :"api_role";
GRANT SELECT (id,name,pixel_id) ON fb_pixels TO :"api_role";
GRANT INSERT (name,pixel_id,access_token), UPDATE (name) ON fb_pixels TO :"api_role";
GRANT USAGE ON SEQUENCE fb_pixels_id_seq TO :"api_role";
GRANT SELECT (id,fb_pixel_id) ON promotion_links TO :"api_role";
GRANT SELECT (event_id,order_id,event_name,state,delivered_at) ON payment_event_outbox TO :"api_role";
GRANT SELECT ON reader_conversion_outbox,reader_conversion_repair_facts TO :"api_role";
GRANT INSERT (business_key,event_name,order_id,environment,occurred_at,created_at,snapshot)
    ON reader_conversion_outbox TO :"api_role";
-- Only used by the repair row lock.
GRANT UPDATE (normalized_at) ON reader_conversion_repair_facts TO :"api_role";
GRANT SELECT ON conversion.integrations,conversion.integration_destinations,conversion.destinations,conversion.events,
    conversion.deliveries,conversion.delivery_attempts,conversion.partner_reports,
    conversion.legacy_outbox_imports,conversion.worker_heartbeats,conversion.alerts TO :"api_role";
GRANT SELECT (report_id,attempt_no,started_at,sending_at,responded_at,recorded_at,outcome,reason,retry_at,http_status,business_code)
    ON conversion.partner_report_attempts TO :"api_role";
GRANT SELECT (id,integration_id,source_order_id,source_event_type,source_event_id,received_at,state,reason)
    ON conversion.inbox TO :"api_role";
GRANT SELECT (id), INSERT (ciphertext,key_version) ON conversion.credentials TO :"api_role";
GRANT INSERT, UPDATE ON conversion.integrations,conversion.destinations,conversion.integration_destinations TO :"api_role";
GRANT DELETE ON conversion.integration_destinations TO :"api_role";
-- PostgreSQL requires one UPDATE column for SELECT FOR UPDATE on an event.
GRANT UPDATE (id) ON conversion.events TO :"api_role";
GRANT INSERT,UPDATE ON conversion.deliveries TO :"api_role";
GRANT UPDATE (state,next_attempt_at,reason) ON conversion.partner_reports TO :"api_role";
GRANT UPDATE (environment_evidence,resolution) ON conversion.legacy_outbox_imports TO :"api_role";
GRANT SELECT, INSERT ON conversion.operation_audit TO :"api_role";

GRANT SELECT (fulfilled_at,capi_expectation) ON recharge_orders TO :"collector_role";
GRANT SELECT (business_key,environment,created_at,consumed_at,processing_error) ON reader_conversion_outbox TO :"collector_role";
GRANT SELECT (id,integration_id,state,received_at) ON conversion.inbox TO :"collector_role";
GRANT SELECT (id,environment,source,account_key,report_config) ON conversion.integrations TO :"collector_role";
GRANT SELECT (id,environment,integration_id,source_event_type,source_event_id,reason,attribution_state,received_at)
    ON conversion.events TO :"collector_role";
GRANT SELECT (id,pixel_id) ON conversion.destinations TO :"collector_role";
GRANT SELECT (id,event_id,role,environment,state,lease_token,deadline_at,next_attempt_at,destination_id,reason)
    ON conversion.deliveries TO :"collector_role";
GRANT SELECT (event_id,legacy_state,legacy_delivered_at) ON conversion.legacy_outbox_imports TO :"collector_role";
GRANT SELECT (integration_id,created_at,state,next_attempt_at) ON conversion.partner_reports TO :"collector_role";
GRANT SELECT ON conversion.worker_heartbeats TO :"collector_role";
GRANT SELECT,INSERT,UPDATE ON conversion.alerts TO :"collector_role";

GRANT EXECUTE ON FUNCTION conversion.lock_active_admin(text) TO :"retention_role";
GRANT SELECT,UPDATE (snapshot) ON reader_conversion_outbox,reader_conversion_repair_facts TO :"retention_role";
GRANT SELECT (id,environment,source,account_key) ON conversion.integrations TO :"retention_role";
GRANT SELECT ON conversion.events TO :"retention_role";
GRANT UPDATE (source_url,matching_data) ON conversion.events TO :"retention_role";
GRANT SELECT (id,event_id,environment,finalized_at) ON conversion.deliveries TO :"retention_role";
GRANT SELECT (id,integration_id,received_at,state,payload),UPDATE (payload) ON conversion.inbox TO :"retention_role";
GRANT SELECT,DELETE,UPDATE (recorded_at) ON conversion.delivery_diagnostics TO :"retention_role";
GRANT SELECT (legacy_event_id,event_id) ON conversion.legacy_outbox_imports TO :"retention_role";
GRANT SELECT (event_id,created_at,payload),UPDATE (payload) ON payment_event_outbox TO :"retention_role";
GRANT INSERT ON conversion.operation_audit TO :"retention_role";

COMMIT;
