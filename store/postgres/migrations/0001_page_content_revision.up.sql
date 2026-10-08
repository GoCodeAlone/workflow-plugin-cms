-- Required host prerequisite for guarded page batches. Apply under a reviewed
-- write fence before adopting the new CMS store; do not mix old/new writers.
BEGIN;
CREATE TABLE cms_page_revisions (
    tenant_id BIGINT PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    content_scope TEXT NOT NULL UNIQUE DEFAULT gen_random_uuid()::text,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0)
);
INSERT INTO cms_page_revisions (tenant_id) SELECT id FROM tenants;
CREATE FUNCTION cms_initialize_page_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO cms_page_revisions (tenant_id) VALUES (NEW.id);
    RETURN NEW;
END;
$$;
CREATE TRIGGER cms_initialize_page_revision
    AFTER INSERT ON tenants FOR EACH ROW EXECUTE FUNCTION cms_initialize_page_revision();
COMMIT;
