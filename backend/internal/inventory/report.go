package inventory

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type ReportFilter struct {
	Tenant, Location, SKU, Actor, Kind string
	From, To                           time.Time
}

type ReportBalance struct {
	Location string `json:"location_id"`
	SKU      string `json:"sku_id"`
	Batch    string `json:"batch_id"`
	Bucket   string `json:"bucket"`
	Quantity string `json:"quantity"`
	Lot      string `json:"lot"`
}

func ProjectBalances(ctx context.Context, tx pgx.Tx, f ReportFilter, after ReportBalance) ([]ReportBalance, error) {
	rows, err := tx.Query(ctx, `SELECT n.location_id,b.sku_id,COALESCE(b.batch_id,'00000000-0000-0000-0000-000000000000'::uuid),
	 b.bucket,b.quantity::text,COALESCE(t.lot_code,'') FROM hive.inventory_balances b
	 JOIN hive.inventory_locations n ON (n.tenant_id,n.id)=(b.tenant_id,b.location_id)
	 LEFT JOIN hive.inventory_batches t ON (t.tenant_id,t.id)=(b.tenant_id,b.batch_id)
	 WHERE b.tenant_id=$1 AND ($2='' OR b.location_id=(SELECT id FROM hive.inventory_locations WHERE tenant_id=$1 AND location_id=NULLIF($2,'')::uuid)) AND ($3='' OR b.sku_id=NULLIF($3,'')::uuid)
	 AND ($4='' OR (b.location_id,b.sku_id,COALESCE(b.batch_id,'00000000-0000-0000-0000-000000000000'::uuid),b.bucket)>
	 ((SELECT id FROM hive.inventory_locations WHERE tenant_id=$1 AND location_id=NULLIF($4,'')::uuid),NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,$7))
	 ORDER BY b.location_id,b.sku_id,COALESCE(b.batch_id,'00000000-0000-0000-0000-000000000000'::uuid),b.bucket LIMIT 51`,
		f.Tenant, f.Location, f.SKU, after.Location, after.SKU, after.Batch, after.Bucket)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ReportBalance])
}

type ReportMovement struct {
	At          time.Time `json:"posted_at"`
	Kind        string    `json:"kind"`
	Document    string    `json:"document_id"`
	Line        int       `json:"line"`
	Location    string    `json:"location_id"`
	SKU         string    `json:"sku_id"`
	Batch       string    `json:"batch_id"`
	Bucket      string    `json:"bucket"`
	Delta       string    `json:"quantity_delta"`
	Actor       string    `json:"actor_id"`
	Lot         string    `json:"lot"`
	SourceBatch string    `json:"source_batch_id"`
}

func ProjectMovements(ctx context.Context, tx pgx.Tx, f ReportFilter, after ReportMovement) ([]ReportMovement, error) {
	var before *time.Time
	if !after.At.IsZero() {
		before = &after.At
	}
	rows, err := tx.Query(ctx, `SELECT p.posted_at,m.kind,m.document_id,m.line,n.location_id,m.sku_id,COALESCE(m.batch_id::text,''),
	 m.bucket,m.delta::text,p.actor_id,COALESCE(b.lot_code,''),COALESCE(l.source_batch_id::text,'')
	 FROM hive.inventory_postings p
	 JOIN hive.inventory_movements m USING(tenant_id,kind,document_id)
	 JOIN hive.inventory_locations n ON (n.tenant_id,n.id)=(m.tenant_id,m.location_id)
	 LEFT JOIN hive.inventory_batches b ON (b.tenant_id,b.id)=(m.tenant_id,m.batch_id)
	 LEFT JOIN hive.inventory_batch_lineage l ON (l.tenant_id,l.output_batch_id,l.repack_id)=(m.tenant_id,m.batch_id,m.document_id) AND m.kind='repacking'
	 WHERE p.tenant_id=$1 AND p.posted_at >= $2 AND p.posted_at < $3
	 AND ($4='' OR n.location_id=NULLIF($4,'')::uuid) AND ($5='' OR m.sku_id=NULLIF($5,'')::uuid)
	 AND ($6='' OR p.actor_id=NULLIF($6,'')::uuid) AND ($7='' OR p.kind=$7)
	 AND ($8::timestamptz IS NULL OR (p.posted_at,p.kind,p.document_id,m.line)<($8,$9,NULLIF($10,'')::uuid,$11))
	 ORDER BY p.posted_at DESC,p.kind DESC,p.document_id DESC,m.line DESC LIMIT 51`,
		f.Tenant, f.From, f.To, f.Location, f.SKU, f.Actor, f.Kind, before, after.Kind, after.Document, after.Line)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ReportMovement])
}

type ReportTransfer struct {
	ID          string `json:"id"`
	From        string `json:"source_location_id"`
	To          string `json:"destination_location_id"`
	Dispatched  string `json:"dispatched"`
	Received    string `json:"received"`
	Transit     string `json:"transit"`
	Discrepancy string `json:"discrepancy"`
}

func ProjectTransfers(ctx context.Context, tx pgx.Tx, f ReportFilter, after string) ([]ReportTransfer, error) {
	rows, err := tx.Query(ctx, `SELECT t.id,d.location_id,COALESCE(d.payload->>'destination_id',''),
	 t.dispatched::text,t.received::text,t.remaining::text,t.discrepancy::text
	 FROM hive.inventory_transfers t JOIN hive.inventory_documents d USING(tenant_id,kind,id)
	 JOIN hive.inventory_postings p ON (p.tenant_id,p.kind,p.document_id)=(t.tenant_id,t.kind,t.id)
	 WHERE t.tenant_id=$1 AND ($2='' OR d.location_id=NULLIF($2,'')::uuid OR d.payload->>'destination_id'=$2)
	 AND ($3='' OR t.id>NULLIF($3,'')::uuid) AND p.posted_at >= $4 AND p.posted_at < $5
	 ORDER BY t.id LIMIT 51`, f.Tenant, f.Location, after, f.From, f.To)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ReportTransfer])
}
