-- Environment-scoped promotion links so sandbox promotions can bind sandbox templates
-- without ever leaking into live production traffic.

ALTER TABLE promotion_links
    ADD COLUMN IF NOT EXISTS environment TEXT NOT NULL DEFAULT 'live'
    CHECK (environment IN ('sandbox', 'live'));

CREATE INDEX IF NOT EXISTS idx_promotion_links_environment ON promotion_links(environment);

-- In live mode (and as fail-closed default for empty/unknown env), only live promotion links may resolve;
-- in explicit sandbox mode, both sandbox and live links resolve for testing (with sandbox preferred for UTM fallback).
CREATE OR REPLACE FUNCTION resolve_reader_promotion_for_env(link INTEGER, source TEXT, campaign TEXT, env TEXT)
RETURNS INTEGER LANGUAGE sql STABLE AS $$
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

CREATE OR REPLACE FUNCTION resolve_reader_promotion(link INTEGER, source TEXT, campaign TEXT)
RETURNS INTEGER LANGUAGE sql STABLE AS $$
 SELECT resolve_reader_promotion_for_env(link, source, campaign, 'live')
$$;

CREATE OR REPLACE FUNCTION reader_promotion_for_user_env(uid BIGINT, env TEXT)
RETURNS SETOF promotion_links LANGUAGE sql STABLE AS $$
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

CREATE OR REPLACE FUNCTION reader_promotion_for_user(uid BIGINT)
RETURNS SETOF promotion_links LANGUAGE sql STABLE AS $$
 SELECT * FROM reader_promotion_for_user_env(uid, 'live')
$$;

INSERT INTO reader_schema_migrations(version) VALUES (2026111936)
ON CONFLICT (version) DO NOTHING;
