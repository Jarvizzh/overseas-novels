-- Migration to convert historical novel IDs (slugs) to standard UUIDs
-- Safely cascades changes across all referencing tables (chapters, bookshelves, unlock_records, recommendations)

BEGIN;

-- 1. Drop existing foreign key constraints that reference novels(id)
ALTER TABLE chapters DROP CONSTRAINT IF EXISTS chapters_novel_id_fkey;
ALTER TABLE bookshelves DROP CONSTRAINT IF EXISTS bookshelves_novel_id_fkey;
ALTER TABLE unlock_records DROP CONSTRAINT IF EXISTS unlock_records_novel_id_fkey;
ALTER TABLE recommendations DROP CONSTRAINT IF EXISTS recommendations_novel_id_fkey;

-- 2. Create temporary table with old slug ID to new random UUID mapping
CREATE TEMP TABLE novel_id_mapping AS
SELECT id AS old_id, gen_random_uuid()::varchar(64) AS new_id
FROM novels;

-- 3. Update referencing tables with new UUID foreign keys
UPDATE chapters c
SET novel_id = m.new_id
FROM novel_id_mapping m
WHERE c.novel_id = m.old_id;

UPDATE bookshelves b
SET novel_id = m.new_id
FROM novel_id_mapping m
WHERE b.novel_id = m.old_id;

UPDATE unlock_records u
SET novel_id = m.new_id
FROM novel_id_mapping m
WHERE u.novel_id = m.old_id;

UPDATE recommendations r
SET novel_id = m.new_id
FROM novel_id_mapping m
WHERE r.novel_id = m.old_id;

-- 4. Update the main novels primary key IDs
UPDATE novels n
SET id = m.new_id
FROM novel_id_mapping m
WHERE n.id = m.old_id;

-- 5. Re-create foreign key constraints with ON UPDATE CASCADE & ON DELETE CASCADE
ALTER TABLE chapters 
  ADD CONSTRAINT chapters_novel_id_fkey 
  FOREIGN KEY (novel_id) REFERENCES novels(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE bookshelves 
  ADD CONSTRAINT bookshelves_novel_id_fkey 
  FOREIGN KEY (novel_id) REFERENCES novels(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE unlock_records 
  ADD CONSTRAINT unlock_records_novel_id_fkey 
  FOREIGN KEY (novel_id) REFERENCES novels(id) ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE recommendations 
  ADD CONSTRAINT recommendations_novel_id_fkey 
  FOREIGN KEY (novel_id) REFERENCES novels(id) ON UPDATE CASCADE ON DELETE CASCADE;

COMMIT;

-- Clean up temp table
DROP TABLE IF EXISTS novel_id_mapping;
