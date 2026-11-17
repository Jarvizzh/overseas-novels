ALTER TABLE users ALTER COLUMN email TYPE VARCHAR(255);
ALTER TABLE bookshelves ADD COLUMN progress_version VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE bookshelves ADD COLUMN shelf_added_at TIMESTAMPTZ;

ALTER TABLE chapters ALTER COLUMN chapter_index DROP NOT NULL;
ALTER TABLE chapters ADD COLUMN content_sha256 TEXT;
ALTER TABLE chapters ADD COLUMN content_object_key TEXT;
ALTER TABLE chapters ADD CONSTRAINT chapter_id_novel_unique UNIQUE (id, novel_id);
-- NULL chapter_index retains retired chapter identities for existing buyers and future restoration.
ALTER TABLE unlock_records ADD COLUMN chapter_id VARCHAR(64);
ALTER TABLE unlock_records ADD COLUMN identity_evidence TEXT;
ALTER TABLE unlock_records ADD COLUMN identity_resolved_at TIMESTAMPTZ;

-- Legacy chapter timestamps are assigned at transaction start. A concurrent
-- import could have an earlier created_at but still commit after the purchase.
-- Only a recoverable commit timestamp can prove the row was already visible.
DO $$
BEGIN
    IF current_setting('track_commit_timestamp') = 'on' THEN
        UPDATE unlock_records u
        SET chapter_id = c.id,
            identity_evidence = 'migration: chapter row committed before purchase',
            identity_resolved_at = now()
        FROM chapters c
        WHERE c.novel_id = u.novel_id AND c.chapter_index = u.chapter_index
          AND c.created_at <= u.created_at AND pg_xact_commit_timestamp(c.xmin) <= u.created_at;
    END IF;
END;
$$;
ALTER TABLE unlock_records DROP CONSTRAINT unique_user_novel_chapter_unlock;
ALTER TABLE unlock_records ADD CONSTRAINT unlock_chapter_identity
    FOREIGN KEY (chapter_id, novel_id) REFERENCES chapters (id, novel_id) DEFERRABLE INITIALLY DEFERRED;
CREATE UNIQUE INDEX unlock_records_user_chapter ON unlock_records (user_id, chapter_id);
CREATE INDEX unlock_records_unresolved ON unlock_records (user_id, novel_id) WHERE chapter_id IS NULL;

CREATE FUNCTION guard_unlock_chapter_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.chapter_id IS NULL OR NULLIF(btrim(NEW.identity_evidence), '') IS NULL
       OR NEW.identity_resolved_at IS NULL THEN
        RAISE EXCEPTION 'chapter identity and resolution evidence are required';
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.chapter_id IS NOT NULL
       AND (NEW.chapter_id, NEW.novel_id, NEW.user_id, NEW.identity_evidence, NEW.identity_resolved_at)
           IS DISTINCT FROM
           (OLD.chapter_id, OLD.novel_id, OLD.user_id, OLD.identity_evidence, OLD.identity_resolved_at) THEN
        RAISE EXCEPTION 'resolved chapter purchase identity cannot be changed';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER unlock_identity_guard BEFORE INSERT OR UPDATE ON unlock_records
FOR EACH ROW EXECUTE FUNCTION guard_unlock_chapter_identity();

-- A split can grant prior buyers access to newly created pieces without granting
-- those pieces to purchases made after the split. New child identities keep this graph acyclic.
CREATE TABLE chapter_entitlement_links (
    chapter_id VARCHAR(64) PRIMARY KEY,
    source_chapter_id VARCHAR(64) NOT NULL CHECK (source_chapter_id <> chapter_id),
    novel_id INT NOT NULL,
    purchased_before TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (chapter_id, novel_id) REFERENCES chapters(id, novel_id) ON DELETE CASCADE,
    FOREIGN KEY (source_chapter_id, novel_id) REFERENCES chapters(id, novel_id) ON DELETE CASCADE
);
CREATE FUNCTION reader_has_chapter_unlock(reader_id BIGINT, target_id VARCHAR) RETURNS BOOLEAN
LANGUAGE sql STABLE AS $$
    WITH RECURSIVE sources(id, cutoff) AS (
        SELECT target_id::varchar(64), 'infinity'::timestamptz
        UNION ALL
        SELECT l.source_chapter_id, LEAST(s.cutoff, l.purchased_before)
        FROM sources s JOIN chapter_entitlement_links l ON l.chapter_id = s.id
    )
    SELECT EXISTS (
        SELECT 1 FROM sources s JOIN unlock_records u ON u.chapter_id = s.id
        WHERE u.user_id = reader_id AND u.created_at <= s.cutoff
    );
$$;

CREATE TABLE chapter_content_objects (
    object_key TEXT PRIMARY KEY,
    chapter_id VARCHAR(64) NOT NULL,
    sha256 TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'staged' CHECK (state IN ('staged','published','retired','deleting','deleted')),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '1 day',
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX chapter_content_cleanup_due ON chapter_content_objects (next_attempt_at)
    WHERE state IN ('staged','retired','deleting');
ALTER TABLE chapters ADD CONSTRAINT chapter_content_object_fk
    FOREIGN KEY (content_object_key) REFERENCES chapter_content_objects (object_key);

CREATE FUNCTION track_chapter_content_reference() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    previous_key TEXT;
    next_key TEXT;
BEGIN
    IF TG_OP <> 'INSERT' THEN previous_key := OLD.content_object_key; END IF;
    IF TG_OP <> 'DELETE' THEN next_key := NEW.content_object_key; END IF;
    IF TG_OP <> 'INSERT' AND previous_key IS NULL
       AND OLD.chapter_index IS NOT NULL AND OLD.content = ''
       AND (TG_OP = 'DELETE' OR NEW.chapter_index IS DISTINCT FROM OLD.chapter_index
            OR NEW.content_object_key IS NOT NULL OR NEW.content <> '') THEN
        INSERT INTO chapter_content_objects (object_key, chapter_id, sha256, state)
        VALUES (OLD.novel_id || '/' || OLD.chapter_index || '.txt', OLD.id, 'legacy', 'retired')
        ON CONFLICT (object_key) DO NOTHING;
    END IF;
    IF previous_key IS NOT DISTINCT FROM next_key THEN RETURN NULL; END IF;
    IF next_key IS NOT NULL THEN
        UPDATE chapter_content_objects SET state = 'published'
        WHERE object_key = next_key AND chapter_id = NEW.id AND sha256 = NEW.content_sha256
          AND state = 'staged' AND next_attempt_at > now();
        IF NOT FOUND THEN RAISE EXCEPTION 'chapter content is no longer staged'; END IF;
    END IF;
    IF previous_key IS NOT NULL THEN
        -- Keep objects long enough for readers using the previous committed version.
        UPDATE chapter_content_objects SET state = 'retired', next_attempt_at = now() + interval '1 day'
        WHERE object_key = previous_key AND state = 'published';
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER chapter_content_reference
AFTER INSERT OR UPDATE OF content_object_key, chapter_index, content OR DELETE ON chapters
FOR EACH ROW EXECUTE FUNCTION track_chapter_content_reference();

INSERT INTO reader_schema_migrations (version) VALUES (2026111702);
