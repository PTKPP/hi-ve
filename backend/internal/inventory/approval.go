package inventory

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"hi-ve/backend/internal/catalog"
	"hi-ve/backend/internal/locations"
)

const (
	AdjustmentApprovePermission  = "inventory.adjustment.approve"
	CountApprovePermission       = "inventory.stock_count.approve"
	DiscrepancyApprovePermission = "inventory.transfer.approve_discrepancy"
)

var ErrStaleSnapshot = errors.New("stock changed after count snapshot")
var ErrSelfApproval = errors.New("self-approval requires Primary Owner")

// Self-approval is evaluated on live membership, independently of the grant.
func checkSeparation(ctx context.Context, tx pgx.Tx, scope Scope, self bool) error {
	if !self {
		return nil
	}
	var owner bool
	err := tx.QueryRow(ctx, `SELECT role='primary_owner' FROM hive.tenant_memberships
        WHERE tenant_id=$1 AND principal_id=$2 AND active FOR SHARE`, scope.Tenant, scope.Actor).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !owner {
		return ErrSelfApproval
	}
	return err
}

func recordApproval(ctx context.Context, tx pgx.Tx, scope Scope, kind, id, requester, location, reason string, self bool, observations []string) error {
	if err := checkSeparation(ctx, tx, scope, self); err != nil {
		return err
	}
	if self && strings.TrimSpace(reason) == "" {
		return ErrInput
	}
	_, err := tx.Exec(ctx, `INSERT INTO hive.inventory_approvals(tenant_id,kind,document_id,requester_id,location_id,reason,observation_ids)
        VALUES($1,$2,$3,$4,$5,$6,$7::text[]::uuid[])`, scope.Tenant, kind, id, requester, location, reason, observations)
	return err
}

func checkRejection(ctx context.Context, tx pgx.Tx, tenant, kind, id string) error {
	var rejected bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hive.inventory_rejections WHERE tenant_id=$1 AND kind=$2 AND document_id=$3)`, tenant, kind, id).Scan(&rejected)
	if err == nil && rejected {
		return ErrConflict
	}
	return err
}

func validApproval(id, reason string) bool {
	return uuidPattern.MatchString(id) && len(reason) <= 255
}

// ApproveAdjustment records approval and posts in the same transaction. There
// is no intermediate approval that could outlive the approver's authorization.
func (s Service) ApproveAdjustment(ctx context.Context, tx pgx.Tx, scope Scope, id, location, reason string) error {
	if !validApproval(id, reason) {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, AdjustmentApprovePermission, location); err != nil {
			return err
		}
		var body Adjustment
		var requester string
		err := tx.QueryRow(ctx, `SELECT payload,created_by FROM hive.inventory_documents
            WHERE tenant_id=$1 AND kind='adjustment' AND id=$2 AND location_id=$3 FOR UPDATE`, scope.Tenant, id, location).Scan(&body, &requester)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		const kind = "adjustment"
		if err = checkRejection(ctx, tx, scope.Tenant, kind, id); err != nil {
			return err
		}
		replay, err := claim(ctx, tx, scope, kind, id, struct{ Location, Reason string }{location, reason})
		if err != nil || replay {
			return err
		}
		node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false)
		if err != nil {
			return err
		}
		quantity, _, err := resolveMeasure(ctx, tx, scope.Tenant, body.Item)
		if err != nil {
			return err
		}
		if err = recordApproval(ctx, tx, scope, kind, id, requester, location, reason, requester == scope.Actor, nil); err != nil {
			return err
		}
		if body.Decrease {
			quantity = "-" + quantity
		}
		return apply(ctx, tx, scope.Tenant, kind, id, []movement{{stockKey{node, body.Item.SKU, body.Item.Batch, "available"}, quantity}})
	})
}

type CountApproval struct {
	Observations []string `json:"observation_ids"`
	Reason       string   `json:"reason"`
}

type countCorrection struct {
	SKU, Batch, Delta string
	Revision          int64
	Observer          string
}

func (s Service) ApproveCount(ctx context.Context, tx pgx.Tx, scope Scope, id, location string, in CountApproval) error {
	if !validApproval(id, in.Reason) || len(in.Observations) == 0 || len(in.Observations) > 100 {
		return ErrInput
	}
	in.Observations = slices.Clone(in.Observations)
	slices.Sort(in.Observations)
	for i, o := range in.Observations {
		if !uuidPattern.MatchString(o) || i > 0 && o == in.Observations[i-1] {
			return ErrInput
		}
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, CountApprovePermission, location); err != nil {
			return err
		}
		var requester string
		var lines int
		err := tx.QueryRow(ctx, `SELECT actor_id,(SELECT count(*) FROM hive.inventory_count_lines l WHERE l.tenant_id=c.tenant_id AND l.count_id=c.id)
            FROM hive.inventory_stock_counts c WHERE tenant_id=$1 AND id=$2 AND location_id=$3 FOR UPDATE`, scope.Tenant, id, location).Scan(&requester, &lines)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if lines != len(in.Observations) {
			return ErrInput
		}
		const kind = "stock_count"
		if err = checkRejection(ctx, tx, scope.Tenant, kind, id); err != nil {
			return err
		}
		replay, err := claim(ctx, tx, scope, kind, id, in)
		if err != nil || replay {
			return err
		}
		node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT l.sku_id,COALESCE(l.batch_id::text,''),trim_scale(o.quantity-l.quantity)::text,l.revision,o.actor_id
            FROM hive.inventory_count_lines l JOIN hive.inventory_count_observations o
            ON (o.tenant_id,o.count_id,o.sku_id)=(l.tenant_id,l.count_id,l.sku_id) AND o.batch_id IS NOT DISTINCT FROM l.batch_id
            WHERE l.tenant_id=$1 AND l.count_id=$2 AND o.id=ANY($3::text[]::uuid[])
            ORDER BY l.sku_id,l.batch_id NULLS FIRST`, scope.Tenant, id, in.Observations)
		if err != nil {
			return err
		}
		corrections, err := pgx.CollectRows(rows, pgx.RowToStructByPos[countCorrection])
		if err != nil {
			return err
		}
		if len(corrections) != lines {
			return ErrInput
		}
		self := requester == scope.Actor
		moves := make([]movement, 0, lines)
		for i, c := range corrections {
			if i > 0 && c.SKU == corrections[i-1].SKU && c.Batch == corrections[i-1].Batch {
				return ErrInput
			}
			if c.Observer == scope.Actor {
				self = true
			}
			if _, err = catalog.SKUForOperation(ctx, tx, scope.Tenant, c.SKU); err != nil {
				return err
			}
			k := stockKey{node, c.SKU, c.Batch, "available"}
			if err = ensureBalance(ctx, tx, scope.Tenant, k); err != nil {
				return err
			}
			var actual int64
			if err = tx.QueryRow(ctx, `SELECT revision FROM hive.inventory_balances WHERE tenant_id=$1 AND location_id=$2
                AND sku_id=$3 AND batch_id IS NOT DISTINCT FROM NULLIF($4,'')::uuid AND bucket=$5 FOR UPDATE`,
				scope.Tenant, k.location, k.sku, k.batch, k.bucket).Scan(&actual); err != nil {
				return err
			}
			if actual != c.Revision {
				return ErrStaleSnapshot
			}
			if c.Delta != "0" {
				moves = append(moves, movement{k, c.Delta})
			}
		}
		if err = recordApproval(ctx, tx, scope, kind, id, requester, location, in.Reason, self, in.Observations); err != nil {
			return err
		}
		return apply(ctx, tx, scope.Tenant, kind, id, moves)
	})
}

type DiscrepancyResolution struct {
	Reason string `json:"reason"`
	Found  bool   `json:"found"`
}

// ResolveDiscrepancy resolves a single recorded discrepancy in full. Found units
// become available at the destination; missing units are written off from discrepancy.
func (s Service) ResolveDiscrepancy(ctx context.Context, tx pgx.Tx, scope Scope, id, location string, in DiscrepancyResolution) error {
	if !validApproval(id, in.Reason) || strings.TrimSpace(in.Reason) == "" {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, DiscrepancyApprovePermission, location); err != nil {
			return err
		}
		const kind = "discrepancy.resolve"
		node, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false)
		if err != nil {
			return err
		}
		var requester, transfer, sku, batch, quantity string
		err = tx.QueryRow(ctx, `SELECT p.actor_id,r.transfer_id,m.sku_id,COALESCE(m.batch_id::text,''),m.delta::text
            FROM hive.inventory_transfer_receipts r
            JOIN hive.inventory_postings p ON(p.tenant_id,p.kind,p.document_id)=(r.tenant_id,r.kind,r.operation_id)
            JOIN hive.inventory_movements m ON(m.tenant_id,m.kind,m.document_id)=(p.tenant_id,p.kind,p.document_id)
            WHERE r.tenant_id=$1 AND r.operation_id=$2 AND r.reason IS NOT NULL AND m.location_id=$3 AND m.bucket='discrepancy'`,
			scope.Tenant, id, node).Scan(&requester, &transfer, &sku, &batch, &quantity)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		replay, err := claim(ctx, tx, scope, kind, id, in)
		if err != nil || replay {
			return err
		}
		if err = recordApproval(ctx, tx, scope, kind, id, requester, location, in.Reason, requester == scope.Actor, nil); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE hive.inventory_transfers SET resolved_discrepancy=resolved_discrepancy+$3::numeric
            WHERE tenant_id=$1 AND id=$2 AND resolved_discrepancy+$3::numeric<=discrepancy`, scope.Tenant, transfer, quantity)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		moves := []movement{{stockKey{node, sku, batch, "discrepancy"}, "-" + quantity}}
		if in.Found {
			moves = append(moves, movement{stockKey{node, sku, batch, "available"}, quantity})
		}
		return apply(ctx, tx, scope.Tenant, kind, id, moves)
	})
}

// RejectCorrection records a scoped decision without creating a stock operation.
func (s Service) RejectCorrection(ctx context.Context, tx pgx.Tx, scope Scope, id, location, reason string, count bool) error {
	if !validApproval(id, reason) || strings.TrimSpace(reason) == "" {
		return ErrInput
	}
	kind, permission := "adjustment", AdjustmentApprovePermission
	lock := `SELECT created_by FROM hive.inventory_documents WHERE tenant_id=$1 AND id=$2 AND location_id=$3 AND kind='adjustment' FOR UPDATE`
	if count {
		kind, permission = "stock_count", CountApprovePermission
		lock = `SELECT actor_id FROM hive.inventory_stock_counts WHERE tenant_id=$1 AND id=$2 AND location_id=$3 FOR UPDATE`
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, permission, location); err != nil {
			return err
		}
		var requester string
		err := tx.QueryRow(ctx, lock, scope.Tenant, id, location).Scan(&requester)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if _, err = locations.PrimaryInventory(ctx, tx, scope.Tenant, location, false); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.inventory_rejections(tenant_id,kind,document_id,actor_id,requester_id,location_id,reason,device_id)
            SELECT $1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid WHERE NOT EXISTS(SELECT 1 FROM hive.inventory_postings WHERE tenant_id=$1 AND kind=$2 AND document_id=$3)
            ON CONFLICT DO NOTHING`, scope.Tenant, kind, id, scope.Actor, requester, location, reason, scope.Device)
		if err != nil || tag.RowsAffected() != 0 {
			return err
		}
		var same bool
		err = tx.QueryRow(ctx, `SELECT actor_id=$4 AND reason=$5 FROM hive.inventory_rejections WHERE tenant_id=$1 AND kind=$2 AND document_id=$3`, scope.Tenant, kind, id, scope.Actor, reason).Scan(&same)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !same {
			return ErrConflict
		}
		return err
	})
}
