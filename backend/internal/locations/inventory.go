package locations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// PrimaryInventory resolves an active resource inside the authorized caller transaction.
// Cashier operations must request an outlet; Inventory may also use warehouses.
func PrimaryInventory(ctx context.Context, tx pgx.Tx, tenant, location string, outletOnly bool) (string, error) {
	var node string
	err := tx.QueryRow(ctx, `SELECT n.id FROM hive.locations l JOIN hive.inventory_locations n ON (n.tenant_id,n.location_id)=(l.tenant_id,l.id)
        JOIN hive.active_tenants t ON t.id=l.tenant_id WHERE l.tenant_id=$1 AND l.id=$2 AND l.status='active'
        AND (NOT $3 OR l.kind='outlet') FOR SHARE OF l,t`, tenant, location, outletOnly).Scan(&node)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrOutletUnavailable
	}
	return node, err
}

type StatusInput struct {
	Status   string `json:"status"`
	Revision *int64 `json:"revision"`
}

func (s Service) SetStatus(ctx context.Context, tenant, token, location, key string, in StatusInput) (int64, error) {
	if in.Revision == nil || *in.Revision < 0 || (in.Status != "active" && in.Status != "inactive") {
		return 0, ErrInput
	}
	var revision int64
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := s.AuthorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		var status string
		err = tx.QueryRow(ctx, `SELECT status,revision FROM hive.locations WHERE tenant_id=$1 AND id=$2 FOR NO KEY UPDATE`, tenant, location).Scan(&status, &revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOutletUnavailable
		}
		if err != nil {
			return err
		}
		var previousID, previousStatus string
		var previousRevision int64
		err = tx.QueryRow(ctx, `SELECT location_id,status,revision FROM hive.location_status_history WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key).Scan(&previousID, &previousStatus, &previousRevision)
		if err == nil {
			if previousID != location || previousStatus != in.Status || previousRevision-1 != *in.Revision {
				return ErrReplay
			}
			revision = previousRevision
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if revision != *in.Revision || status == in.Status {
			return ErrReplay
		}
		if in.Status == "inactive" {
			if err = s.EndOutletContexts(ctx, tx, tenant, location); err != nil {
				return err
			}
		}
		err = tx.QueryRow(ctx, `UPDATE hive.locations SET status=$3,revision=revision+1 WHERE tenant_id=$1 AND id=$2 RETURNING revision`, tenant, location, in.Status).Scan(&revision)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.location_status_history(tenant_id,location_id,actor_id,request_key,revision,status)
            VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id,actor_id,request_key) DO NOTHING`, tenant, location, actor, key, revision, in.Status)
		if err == nil && tag.RowsAffected() != 1 {
			return ErrReplay
		}
		return err
	})
	return revision, err
}

type Mapping struct {
	InventoryLocation string `json:"inventory_location_id"`
	Status            string `json:"status"`
	Revision          int64  `json:"revision"`
}

func (s Service) InventoryMapping(ctx context.Context, tenant, token, location string) (Mapping, error) {
	var result Mapping
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT n.id,l.status,l.revision FROM hive.locations l JOIN hive.inventory_locations n
            ON (n.tenant_id,n.location_id)=(l.tenant_id,l.id) WHERE l.tenant_id=$1 AND l.id=$2`, tenant, location).Scan(&result.InventoryLocation, &result.Status, &result.Revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOutletUnavailable
		}
		return err
	})
	return result, err
}
