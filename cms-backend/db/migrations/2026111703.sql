ALTER TABLE chapters
    ADD COLUMN content_preview TEXT,
    ADD COLUMN body_revision BIGINT NOT NULL DEFAULT 0;

CREATE FUNCTION advance_reader_body_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.content_object_key, NEW.content_sha256, NEW.content)
        IS DISTINCT FROM (OLD.content_object_key, OLD.content_sha256, OLD.content) THEN
        NEW.body_revision := OLD.body_revision + 1;
        IF NEW.chapter_index IS NOT NULL AND NEW.content_object_key IS NULL
            AND NEW.content IS DISTINCT FROM OLD.content
            AND (NEW.content_object_key, NEW.content_sha256)
                IS NOT DISTINCT FROM (OLD.content_object_key, OLD.content_sha256) THEN
            NEW.content_sha256 := NULL;
            NEW.content_preview := NULL;
        END IF;
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER reader_body_revision BEFORE UPDATE OF content, content_object_key, content_sha256 ON chapters
FOR EACH ROW EXECUTE FUNCTION advance_reader_body_revision();

-- The release keeps inline legacy previews available without rewriting the chapter table.
-- Legacy OSS objects are handled by the CMS preview backfill.
CREATE FUNCTION reader_chapter_preview(body TEXT) RETURNS TEXT
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT CASE WHEN body = '' THEN ''
        ELSE left(body, LEAST(250, char_length(body) / 2)) || '...' END
$$;

CREATE OR REPLACE FUNCTION bump_reader_novel_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.content_version IS DISTINCT FROM OLD.content_version
        OR (NEW.status, NEW.publish_status) IS DISTINCT FROM (OLD.status, OLD.publish_status) THEN
        NEW.content_version := OLD.content_version + 1;
    END IF;
    RETURN NEW;
END $$;

DROP TRIGGER reader_chapter_version ON chapters;
CREATE FUNCTION bump_reader_chapter_statement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- A CMS publication updates the novel's word count and generation together.
    IF current_setting('star_novel.publishing_chapters', true) = 'on' THEN
        RETURN NULL;
    END IF;
    IF TG_OP = 'INSERT' THEN
        UPDATE novels SET content_version = content_version + 1
        WHERE id IN (SELECT novel_id FROM new_chapters WHERE chapter_index IS NOT NULL);
    ELSIF TG_OP = 'DELETE' THEN
        UPDATE novels SET content_version = content_version + 1
        WHERE id IN (SELECT novel_id FROM old_chapters WHERE chapter_index IS NOT NULL);
    ELSE
        UPDATE novels SET content_version = content_version + 1 WHERE id IN (
            SELECT DISTINCT unnest(ARRAY[o.novel_id, n.novel_id])
            FROM old_chapters o FULL JOIN new_chapters n USING (id)
            WHERE (o.chapter_index IS NOT NULL OR n.chapter_index IS NOT NULL)
              AND (o.novel_id, o.chapter_index, o.title, o.word_count, o.body_revision)
                  IS DISTINCT FROM (n.novel_id, n.chapter_index, n.title, n.word_count, n.body_revision)
        );
    END IF;
    RETURN NULL;
END $$;

CREATE TRIGGER reader_chapter_insert_version AFTER INSERT ON chapters
REFERENCING NEW TABLE AS new_chapters FOR EACH STATEMENT EXECUTE FUNCTION bump_reader_chapter_statement();
CREATE TRIGGER reader_chapter_delete_version AFTER DELETE ON chapters
REFERENCING OLD TABLE AS old_chapters FOR EACH STATEMENT EXECUTE FUNCTION bump_reader_chapter_statement();
CREATE TRIGGER reader_chapter_update_version AFTER UPDATE ON chapters
REFERENCING OLD TABLE AS old_chapters NEW TABLE AS new_chapters
FOR EACH STATEMENT EXECUTE FUNCTION bump_reader_chapter_statement();

INSERT INTO reader_schema_migrations(version) VALUES (2026111703);
