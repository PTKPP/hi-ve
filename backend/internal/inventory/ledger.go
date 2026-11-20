// Package inventory owns authoritative stock postings and balances.
package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInput       = errors.New("invalid inventory input")
	ErrConflict    = errors.New("conflicting inventory document or operation")
	ErrShortage    = errors.New("insufficient stock")
	uuidPattern    = regexp.MustCompile("^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$")
	decimalPattern = regexp.MustCompile("^(0|[1-9][0-9]*)([.][0-9]+)?$")
)

// Scope is derived from the authenticated use case and checked against live policy.
type Scope struct {
	Tenant string
	Actor  string
}

// Authorize checks the applicable explicit user permission and entitlement inside
// the transaction. The location is a Locations master ID, never a stock node ID.
type Authorize func(ctx context.Context, tx pgx.Tx, scope Scope, action, location string) error

type Service struct {
	Authorize Authorize
}

type Measure struct {
	SKU      string `json:"sku_id"`
	Batch    string `json:"batch_id"`
	Quantity string `json:"quantity"`
	Unit     string `json:"unit"`
}

type stockKey struct {
	location, sku, batch, bucket string
}

type movement struct {
	key   stockKey
	delta string
}

func (s Service) authorized(ctx context.Context, tx pgx.Tx, scope Scope, action, location string) error {
	if !uuidPattern.MatchString(scope.Tenant) || !uuidPattern.MatchString(scope.Actor) || !uuidPattern.MatchString(location) || s.Authorize == nil {
		return ErrInput
	}
	return s.Authorize(ctx, tx, scope, action, location)
}

// claim records the business operation, independent of transport request identity.
// Replays must still pass the use case's live authorization.
func claim(ctx context.Context, tx pgx.Tx, scope Scope, kind, id string, input any) (bool, error) {
	if !uuidPattern.MatchString(id) {
		return false, ErrInput
	}
	data, err := json.Marshal(input)
	if err != nil {
		return false, err
	}
	hash := sha256.Sum256(data)
	tag, err := tx.Exec(ctx, `INSERT INTO hive.inventory_postings(tenant_id,kind,document_id,actor_id,fingerprint)
        VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, scope.Tenant, kind, id, scope.Actor, hash[:])
	if err != nil || tag.RowsAffected() != 0 {
		return false, err
	}
	var previous []byte
	var actor string
	err = tx.QueryRow(ctx, `SELECT actor_id,fingerprint FROM hive.inventory_postings WHERE tenant_id=$1 AND kind=$2 AND document_id=$3`,
		scope.Tenant, kind, id).Scan(&actor, &previous)
	if err == nil && (scope.Actor != actor || !bytes.Equal(previous, hash[:])) {
		return false, ErrConflict
	}
	return true, err
}

// apply acquires all touched balance keys in the same order across workflows,
// including keys whose balance rows have not been created yet.
func apply(ctx context.Context, tx pgx.Tx, tenant, kind, id string, movements []movement) error {
	compare := func(a, b stockKey) int {
		for _, pair := range [][2]string{{a.location, b.location}, {a.sku, b.sku}, {a.batch, b.batch}, {a.bucket, b.bucket}} {
			if n := strings.Compare(pair[0], pair[1]); n != 0 {
				return n
			}
		}
		return 0
	}
	slices.SortStableFunc(movements, func(a, b movement) int { return compare(a.key, b.key) })
	for i, move := range movements {
		k := move.key
		if i == 0 || movements[i-1].key != k {
			if err := ensureBalance(ctx, tx, tenant, k); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE hive.inventory_balances SET quantity=quantity+$6::numeric,revision=revision+1
            WHERE tenant_id=$1 AND location_id=$2 AND sku_id=$3 AND batch_id IS NOT DISTINCT FROM NULLIF($4,'')::uuid
            AND bucket=$5 AND quantity+$6::numeric>=0`, tenant, k.location, k.sku, k.batch, k.bucket, move.delta)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrShortage
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.inventory_movements(tenant_id,kind,document_id,line,location_id,sku_id,batch_id,bucket,delta)
            VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid,$8,$9::numeric)`, tenant, kind, id, i+1, k.location, k.sku, k.batch, k.bucket, move.delta); err != nil {
			return err
		}
	}
	return nil
}

func ensureBalance(ctx context.Context, tx pgx.Tx, tenant string, k stockKey) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.inventory_balances(tenant_id,location_id,sku_id,batch_id,bucket,quantity)
        VALUES($1,$2,$3,NULLIF($4,'')::uuid,$5,0) ON CONFLICT DO NOTHING`, tenant, k.location, k.sku, k.batch, k.bucket)
	return err
}
