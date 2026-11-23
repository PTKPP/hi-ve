package inventory

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"hi-ve/backend/internal/locations"
)

const (
	TransferDraftPermission       = "inventory.transfer.create"
	TransferSendPermission        = "inventory.transfer.send"
	TransferQuickPermission       = "inventory.transfer.quick"
	TransferReceiptPermission     = "inventory.transfer.receive"
	TransferDiscrepancyPermission = "inventory.transfer.record_discrepancy"
)

type Transfer struct {
	Destination string  `json:"destination_id"`
	Item        Measure `json:"item"`
	Quick       bool    `json:"quick"`
}

func (s Service) authorizeRoute(ctx context.Context, tx pgx.Tx, scope Scope, action, source, destination string) error {
	if source == destination {
		return ErrInput
	}
	ids := []string{source, destination}
	slices.Sort(ids)
	for _, id := range ids {
		if err := s.authorized(ctx, tx, scope, action, id); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) DraftTransfer(ctx context.Context, tx pgx.Tx, scope Scope, id, source string, transfer Transfer) error {
	if err := s.authorizeRoute(ctx, tx, scope, TransferDraftPermission, source, transfer.Destination); err != nil {
		return err
	}
	if _, err := resolveRoute(ctx, tx, scope.Tenant, source, transfer.Destination); err != nil {
		return err
	}
	if _, _, err := resolveMeasure(ctx, tx, scope.Tenant, transfer.Item); err != nil {
		return err
	}
	if err := ensureBatch(ctx, tx, scope.Tenant, transfer.Item); err != nil {
		return err
	}
	return createDocument(ctx, tx, scope, "transfer", id, source, transfer)
}

func loadTransfer(ctx context.Context, tx pgx.Tx, tenant, id, source string) (Transfer, error) {
	var transfer Transfer
	err := tx.QueryRow(ctx, `SELECT payload FROM hive.inventory_documents WHERE tenant_id=$1 AND kind='transfer' AND id=$2 AND location_id=$3`, tenant, id, source).Scan(&transfer)
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer, ErrConflict
	}
	return transfer, err
}

func resolveRoute(ctx context.Context, tx pgx.Tx, tenant, source, destination string) (map[string]string, error) {
	ids := []string{source, destination}
	slices.Sort(ids)
	nodes := make(map[string]string, 2)
	for _, id := range ids {
		node, err := locations.PrimaryInventory(ctx, tx, tenant, id, false)
		if err != nil {
			return nil, err
		}
		nodes[id] = node
	}
	return nodes, nil
}

// DispatchTransfer posts a normal dispatch to transit or a quick transfer directly
// to destination, using the mode stored on the draft.
func (s Service) DispatchTransfer(ctx context.Context, tx pgx.Tx, scope Scope, id, source string) error {
	if !uuidPattern.MatchString(id) || !uuidPattern.MatchString(scope.Tenant) || !uuidPattern.MatchString(source) {
		return ErrInput
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		transfer, err := loadTransfer(ctx, tx, scope.Tenant, id, source)
		if err != nil {
			return err
		}
		action := TransferSendPermission
		if transfer.Quick {
			action = TransferQuickPermission
		}
		if err = s.authorizeRoute(ctx, tx, scope, action, source, transfer.Destination); err != nil {
			return err
		}
		const kind = transferDispatchKind
		replay, err := claim(ctx, tx, scope, kind, id, transfer)
		if err != nil || replay {
			return err
		}
		nodes, err := resolveRoute(ctx, tx, scope.Tenant, source, transfer.Destination)
		if err != nil {
			return err
		}
		quantity, _, err := resolveMeasure(ctx, tx, scope.Tenant, transfer.Item)
		if err != nil {
			return err
		}
		if err = ensureBatch(ctx, tx, scope.Tenant, transfer.Item); err != nil {
			return err
		}
		target, bucket := nodes[source], "in_transit"
		remaining, received := quantity, "0"
		if transfer.Quick {
			target, bucket = nodes[transfer.Destination], "available"
			remaining, received = "0", quantity
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.inventory_transfers(tenant_id,id,source_node,dispatched,remaining,received)
            VALUES($1,$2,$3,$4::numeric,$5::numeric,$6::numeric)`, scope.Tenant, id, nodes[source], quantity, remaining, received); err != nil {
			return err
		}
		item := transfer.Item
		return apply(ctx, tx, scope.Tenant, kind, id, []movement{
			{stockKey{nodes[source], item.SKU, item.Batch, "available"}, "-" + quantity},
			{stockKey{target, item.SKU, item.Batch, bucket}, quantity},
		})
	})
}

type TransferReceipt struct {
	Transfer          string
	Source            string
	Destination       string `json:"destination_id"`
	Quantity          string
	Unit              string
	DiscrepancyReason string
}

// ReceiveTransfer adds only accepted units to the destination. A documented
// discrepancy goes into its own bucket and remains unresolved.
func (s Service) ReceiveTransfer(ctx context.Context, tx pgx.Tx, scope Scope, operation string, in TransferReceipt) error {
	if !uuidPattern.MatchString(in.Transfer) || !uuidPattern.MatchString(in.Source) || len(in.DiscrepancyReason) > 255 {
		return ErrInput
	}
	const kind = "transfer.receipt"
	action := TransferReceiptPermission
	discrepancy := in.DiscrepancyReason != ""
	if discrepancy {
		if strings.TrimSpace(in.DiscrepancyReason) == "" {
			return ErrInput
		}
		action = TransferDiscrepancyPermission
	}
	return pgx.BeginFunc(ctx, tx, func(tx pgx.Tx) error {
		if err := s.authorized(ctx, tx, scope, action, in.Destination); err != nil {
			return err
		}
		transfer, err := loadTransfer(ctx, tx, scope.Tenant, in.Transfer, in.Source)
		if err != nil {
			return err
		}
		if transfer.Quick || transfer.Destination != in.Destination {
			return ErrConflict
		}
		replay, err := claim(ctx, tx, scope, kind, operation, in)
		if err != nil || replay {
			return err
		}
		targetNode, err := locations.PrimaryInventory(ctx, tx, scope.Tenant, in.Destination, false)
		if err != nil {
			return err
		}
		item := transfer.Item
		item.Quantity, item.Unit = in.Quantity, in.Unit
		quantity, _, err := resolveMeasure(ctx, tx, scope.Tenant, item)
		if err != nil {
			return err
		}
		var sourceNode string
		err = tx.QueryRow(ctx, `UPDATE hive.inventory_transfers
            SET remaining=remaining-$3::numeric, received=received+CASE WHEN $4 THEN 0 ELSE $3::numeric END,
                discrepancy=discrepancy+CASE WHEN $4 THEN $3::numeric ELSE 0 END
            WHERE tenant_id=$1 AND id=$2 AND remaining >= $3::numeric RETURNING source_node`, scope.Tenant, in.Transfer, quantity, discrepancy).Scan(&sourceNode)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.inventory_transfer_receipts(tenant_id,kind,operation_id,transfer_id,reason)
            VALUES($1,$2,$3,$4,NULLIF($5,''))`, scope.Tenant, kind, operation, in.Transfer, in.DiscrepancyReason); err != nil {
			return err
		}
		bucket := "available"
		if discrepancy {
			bucket = "discrepancy"
		}
		return apply(ctx, tx, scope.Tenant, kind, operation, []movement{
			{stockKey{sourceNode, item.SKU, item.Batch, "in_transit"}, "-" + quantity},
			{stockKey{targetNode, item.SKU, item.Batch, bucket}, quantity},
		})
	})
}
