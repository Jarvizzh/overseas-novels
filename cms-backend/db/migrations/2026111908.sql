ALTER TABLE conversion.partner_report_attempts
    ADD COLUMN http_status smallint CHECK (http_status BETWEEN 100 AND 599),
    ADD COLUMN business_code bigint,
    ADD COLUMN responded_at timestamptz;

CREATE OR REPLACE FUNCTION conversion.sinan_route_issue(event_key bigint, destination_key bigint)
RETURNS text LANGUAGE sql STABLE AS $$
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

DROP FUNCTION conversion.promotion_pixel_mismatch(bigint,text);

INSERT INTO reader_schema_migrations (version) VALUES (2026111908);
