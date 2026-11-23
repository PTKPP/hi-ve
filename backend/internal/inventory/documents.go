package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"hi-ve/backend/internal/locations"
)

const (
	ReceiveDraftPermission = "inventory.receiving.create"
	RepackDraftPermission  = "inventory.repacking.create"
)

type Receiving struct {
	Item        Measure `json:"item"`
	Lot         string  `json:"lot"`
	Origin      string  `json:"origin"`
	ReceivedOn  string  `json:"received_on"`
	NonSaleable bool    `json:"non_saleable"`
}

type Repacking struct {
	Source       Measure `json:"source"`
	Output       Measure `json:"output"`
	Lot          string  `json:"lot"`
	VarianceNote string  `json:"variance_note"`
}

func createDocument(ctx context.Context, tx pgx.Tx, scope Scope, kind, id, location string, body any) error {
	if !uuidPattern.MatchString(id) {
		return ErrInput
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO hive.inventory_documents
        (tenant_id,kind,id,location_id,created_by,payload) VALUES($1,$2,$3,$4,$5,$6)
        ON CONFLICT DO NOTHING`, scope.Tenant, kind, id, location, scope.Actor, payload)
	if err != nil || tag.RowsAffected() != 0 {
		return err
	}
	var same bool
	err = tx.QueryRow(ctx, `SELECT location_id=$4 AND created_by=$5 AND payload=$6
        FROM hive.inventory_documents WHERE tenant_id=$1 AND kind=$2 AND id=$3`,
		scope.Tenant, kind, id, location, scope.Actor, payload).Scan(&same)
	if err == nil && !same {
		return ErrConflict
	}
	return err
}

func (s Service) DraftReceiving(ctx context.Context, tx pgx.Tx, scope Scope, id, location string, body Receiving) error {
	if err := s.authorized(ctx, tx, scope, ReceiveDraftPermission, location); err != nil {
		return err
	}
	if _, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false); err != nil {
		return err
	}
	_, tracked, err := resolveMeasure(ctx, tx, scope.Tenant, body.Item)
	if err != nil {
		return err
	}
	if _, err = receiptDate(body, tracked); err != nil {
		return err
	}
	return createDocument(ctx, tx, scope, "receiving", id, location, body)
}

func (s Service) DraftRepacking(ctx context.Context, tx pgx.Tx, scope Scope, id, location string, body Repacking) error {
	if err := s.authorized(ctx, tx, scope, RepackDraftPermission, location); err != nil {
		return err
	}
	if _, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false); err != nil {
		return err
	}
	if !validRepack(body) {
		return ErrInput
	}
	for _, item := range []Measure{body.Source, body.Output} {
		if _, _, err := resolveMeasure(ctx, tx, scope.Tenant, item); err != nil {
			return err
		}
	}
	if err := ensureBatch(ctx, tx, scope.Tenant, body.Source); err != nil {
		return err
	}
	return createDocument(ctx, tx, scope, "repacking", id, location, body)
}

func (s Service) postDocument(ctx context.Context, tx pgx.Tx, scope Scope, kind, id, location, permission string, body any, post func(pgx.Tx, string) error) error {
	if !uuidPattern.MatchString(id) {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, permission, location); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT payload FROM hive.inventory_documents WHERE tenant_id=$1 AND kind=$2 AND id=$3 AND location_id=$4`,
			scope.Tenant, kind, id, location).Scan(body)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		replay, err := claim(ctx, tx, scope, kind, id, body)
		if err != nil || replay {
			return err
		}
		node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false)
		if err != nil {
			return err
		}
		return post(tx, node)
	})
}

func newBatch(ctx context.Context, tx pgx.Tx, tenant string, item Measure, lot, origin string, receivedOn time.Time) error {
	if strings.TrimSpace(lot) == "" || len(lot) > 128 || strings.TrimSpace(origin) == "" || len(origin) > 255 {
		return ErrInput
	}
	_, err := tx.Exec(ctx, `INSERT INTO hive.inventory_batches(tenant_id,id,sku_id,lot_code,origin,received_on)
        VALUES($1,$2,$3,$4,$5,$6)`, tenant, item.Batch, item.SKU, lot, origin, receivedOn)
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func (s Service) PostReceiving(ctx context.Context, tx pgx.Tx, scope Scope, id, location string) error {
	var body Receiving
	return s.postDocument(ctx, tx, scope, "receiving", id, location, ReceivePermission, &body, func(tx pgx.Tx, node string) error {
		quantity, tracked, err := resolveMeasure(ctx, tx, scope.Tenant, body.Item)
		if err != nil {
			return err
		}
		receivedOn, err := receiptDate(body, tracked)
		if err != nil {
			return err
		}
		if tracked {
			if err = newBatch(ctx, tx, scope.Tenant, body.Item, body.Lot, body.Origin, receivedOn); err != nil {
				return err
			}
		}
		bucket := "available"
		if body.NonSaleable {
			bucket = "non_saleable"
		}
		return apply(ctx, tx, scope.Tenant, "receiving", id, []movement{{stockKey{node, body.Item.SKU, body.Item.Batch, bucket}, quantity}})
	})
}

func (s Service) PostRepacking(ctx context.Context, tx pgx.Tx, scope Scope, id, location string) error {
	var body Repacking
	return s.postDocument(ctx, tx, scope, "repacking", id, location, RepackPermission, &body, func(tx pgx.Tx, node string) error {
		if !validRepack(body) {
			return ErrInput
		}
		input, _, err := resolveMeasure(ctx, tx, scope.Tenant, body.Source)
		if err != nil {
			return err
		}
		output, _, err := resolveMeasure(ctx, tx, scope.Tenant, body.Output)
		if err != nil {
			return err
		}
		if err = ensureBatch(ctx, tx, scope.Tenant, body.Source); err != nil {
			return err
		}
		if err = newBatch(ctx, tx, scope.Tenant, body.Output, body.Lot, "repacking", time.Now().UTC()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.inventory_batch_lineage(tenant_id,output_batch_id,source_batch_id,repack_id)
            VALUES($1,$2,$3,$4)`, scope.Tenant, body.Output.Batch, body.Source.Batch, id); err != nil {
			return err
		}
		return apply(ctx, tx, scope.Tenant, "repacking", id, []movement{
			{stockKey{node, body.Source.SKU, body.Source.Batch, "available"}, "-" + input},
			{stockKey{node, body.Output.SKU, body.Output.Batch, "available"}, output},
		})
	})
}

func receiptDate(body Receiving, tracked bool) (time.Time, error) {
	received, err := time.Parse(time.DateOnly, body.ReceivedOn)
	if err != nil || strings.TrimSpace(body.Origin) == "" || len(body.Origin) > 255 ||
		(tracked && (strings.TrimSpace(body.Lot) == "" || len(body.Lot) > 128)) {
		return time.Time{}, ErrInput
	}
	return received, nil
}

func validRepack(body Repacking) bool {
	return body.Source.Batch != "" && body.Output.Batch != "" && body.Source.Batch != body.Output.Batch &&
		len(body.VarianceNote) <= 255 && strings.TrimSpace(body.Lot) != "" && len(body.Lot) <= 128
}
