-- Explicit additional host prerequisite, after 0001 and under a write fence.
-- No fabricated backfill: history begins after each current tenant revision.
BEGIN;
ALTER TABLE cms_page_revisions ADD COLUMN history_start_revision BIGINT NOT NULL DEFAULT 0 CHECK (history_start_revision >= 0 AND history_start_revision <= revision);
UPDATE cms_page_revisions SET history_start_revision = revision;
CREATE TABLE cms_page_history (
    tenant_id BIGINT NOT NULL REFERENCES cms_page_revisions(tenant_id),
    content_scope TEXT NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    committed_at TIMESTAMPTZ NOT NULL,
    actor TEXT NOT NULL CHECK (length(actor) BETWEEN 1 AND 256),
    operation TEXT NOT NULL CHECK (operation IN ('create','update','delete','batch.apply','batch.rollback')),
    before_pages JSONB NOT NULL CHECK (jsonb_typeof(before_pages) = 'array'),
    after_pages JSONB NOT NULL CHECK (jsonb_typeof(after_pages) = 'array'),
    digest TEXT NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (tenant_id, revision)
);
CREATE FUNCTION cms_refuse_history_rewrite() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'saved page history is append-only';
END;
$$;
CREATE TRIGGER cms_refuse_history_rewrite BEFORE UPDATE OR DELETE ON cms_page_history
    FOR EACH ROW EXECUTE FUNCTION cms_refuse_history_rewrite();
CREATE TRIGGER cms_refuse_history_truncate BEFORE TRUNCATE ON cms_page_history
    FOR EACH STATEMENT EXECUTE FUNCTION cms_refuse_history_rewrite();
COMMIT;
