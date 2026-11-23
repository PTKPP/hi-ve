-- Drafts carry measurements and create no stock. Posting links movements to these documents.
CREATE TABLE hive.inventory_documents (
    tenant_id uuid NOT NULL, kind text NOT NULL CHECK(kind IN ('receiving','repacking')),
    id uuid NOT NULL, location_id uuid NOT NULL, created_by uuid NOT NULL,
    payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,kind,id),
    FOREIGN KEY(tenant_id,location_id) REFERENCES hive.locations,
    FOREIGN KEY(tenant_id,created_by) REFERENCES hive.tenant_memberships
);
CREATE TRIGGER inventory_documents_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_documents FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();

CREATE TABLE hive.inventory_batch_lineage (
    tenant_id uuid NOT NULL, output_batch_id uuid NOT NULL, source_batch_id uuid NOT NULL,
    repack_id uuid NOT NULL, kind text GENERATED ALWAYS AS ('repacking'::text) STORED,
    PRIMARY KEY (tenant_id,output_batch_id), UNIQUE(tenant_id,repack_id),
    CHECK (output_batch_id<>source_batch_id),
    FOREIGN KEY (tenant_id,output_batch_id) REFERENCES hive.inventory_batches,
    FOREIGN KEY (tenant_id,source_batch_id) REFERENCES hive.inventory_batches,
    FOREIGN KEY (tenant_id,kind,repack_id) REFERENCES hive.inventory_postings
);
CREATE TRIGGER inventory_lineage_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_batch_lineage FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
CREATE TRIGGER inventory_batches_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_batches FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
