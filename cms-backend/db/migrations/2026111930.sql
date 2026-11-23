-- Template, catalog revision, and promotion link integrity fixes (2026-11-23).

-- 1. Track per-binding Stripe Price revision so 24h Idempotency-Key never collides when toggling tax_behavior (F-01).
ALTER TABLE product_channel_bindings
    ADD COLUMN IF NOT EXISTS catalog_revision INT NOT NULL DEFAULT 1;

-- 2. Tag recharge templates with their target environment so sandbox-only products cannot leak into live templates (F-05).
ALTER TABLE recharge_templates
    ADD COLUMN IF NOT EXISTS environment TEXT NOT NULL DEFAULT 'live'
    CHECK (environment IN ('sandbox', 'live'));

-- 3. Prevent silent ON DELETE SET NULL unlinking when a template is referenced by promotion_links (F-06).
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'promotion_links'::regclass
          AND contype = 'f'
          AND confrelid = 'recharge_templates'::regclass
    LOOP
        EXECUTE format('ALTER TABLE promotion_links DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE promotion_links
    ADD CONSTRAINT promotion_links_recharge_template_id_fkey
    FOREIGN KEY (recharge_template_id) REFERENCES recharge_templates(id) ON DELETE RESTRICT;

-- 4. Keep whole_book_offer_available isolated from any non-live recharge templates (R-1).
CREATE OR REPLACE FUNCTION whole_book_offer_available(p_user_id BIGINT, p_template_id INTEGER, p_novel_id BIGINT)
RETURNS BOOLEAN LANGUAGE sql STABLE AS $$
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

INSERT INTO reader_schema_migrations(version) VALUES (2026111930)
ON CONFLICT (version) DO NOTHING;
