SET LOCAL lock_timeout='5s';
ALTER TABLE hive.inventory_documents ADD CONSTRAINT inventory_document_kinds
    CHECK (kind IN ('receiving','repacking','transfer')) NOT VALID;
ALTER TABLE hive.inventory_documents VALIDATE CONSTRAINT inventory_document_kinds;
ALTER TABLE hive.inventory_documents DROP CONSTRAINT inventory_documents_kind_check;
CREATE TABLE hive.inventory_transfers (
    tenant_id uuid NOT NULL, id uuid NOT NULL, source_node uuid NOT NULL,
    kind text GENERATED ALWAYS AS ('transfer'::text) STORED,
    dispatched numeric NOT NULL CHECK (dispatched>0 AND dispatched<'Infinity'::numeric),
    remaining numeric NOT NULL CHECK (remaining>=0),
    received numeric NOT NULL DEFAULT 0 CHECK (received>=0),
    discrepancy numeric NOT NULL DEFAULT 0 CHECK(discrepancy>=0),
    CHECK (remaining+received+discrepancy=dispatched),
    PRIMARY KEY (tenant_id,id),
    FOREIGN KEY (tenant_id,kind,id) REFERENCES hive.inventory_documents,
    FOREIGN KEY (tenant_id,source_node) REFERENCES hive.inventory_locations
);
CREATE TABLE hive.inventory_transfer_receipts (
    tenant_id uuid NOT NULL, kind text NOT NULL CHECK(kind='transfer.receipt'),
    operation_id uuid NOT NULL, transfer_id uuid NOT NULL,
    reason text CHECK (reason IS NULL OR length(btrim(reason)) BETWEEN 1 AND 255),
    PRIMARY KEY (tenant_id,kind,operation_id),
    FOREIGN KEY (tenant_id,kind,operation_id) REFERENCES hive.inventory_postings,
    FOREIGN KEY (tenant_id,transfer_id) REFERENCES hive.inventory_transfers
);
CREATE TRIGGER inventory_transfer_receipts_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_transfer_receipts FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
