package reports

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/inventory"
	"hi-ve/backend/internal/platform/entitlements"
	"hi-ve/backend/internal/sales"
)

var ErrInput = errors.New("invalid report filter")
var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Service struct {
	DB           *pgxpool.Pool
	Entitlements entitlements.Service
}

type Filter struct {
	Tenant, Location, SKU, Actor, Movement, Cursor string
	From, To                                       time.Time
}

type Dashboard struct {
	Updated time.Time           `json:"updated_at"`
	Total   sales.ReportTotals  `json:"total"`
	Groups  []sales.ReportGroup `json:"groups"`
}

type Page[T any] struct {
	Rows   []T    `json:"rows"`
	Cursor string `json:"next_cursor,omitempty"`
}

func cursor[T any](f Filter, kind string) (T, error) {
	var key T
	if f.Cursor == "" {
		return key, nil
	}
	if len(f.Cursor) > 2048 {
		return key, ErrInput
	}
	raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
	var c struct {
		Kind, Tenant, Location, SKU, Actor, Movement string
		From, To                                     time.Time
		Key                                          T
	}
	if err != nil || json.Unmarshal(raw, &c) != nil || c.Kind != kind || c.Tenant != f.Tenant || c.Location != f.Location ||
		c.SKU != f.SKU || c.Actor != f.Actor || c.Movement != f.Movement || !c.From.Equal(f.From) || !c.To.Equal(f.To) {
		return key, ErrInput
	}
	return c.Key, nil
}
func page[T, K any](f Filter, kind string, rows []T, key func(T) K) Page[T] {
	if rows == nil {
		rows = []T{}
	}
	out := Page[T]{Rows: rows}
	if len(rows) > 50 {
		out.Rows = rows[:50]
		raw, _ := json.Marshal(struct {
			Kind, Tenant, Location, SKU, Actor, Movement string
			From, To                                     time.Time
			Key                                          K
		}{
			kind, f.Tenant, f.Location, f.SKU, f.Actor, f.Movement, f.From, f.To, key(out.Rows[49])})
		out.Cursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out
}
func allowedInput(f Filter, kind string) bool {
	if !uuid.MatchString(f.Tenant) {
		return false
	}
	for _, id := range []string{f.Location, f.SKU, f.Actor} {
		if id != "" && !uuid.MatchString(id) {
			return false
		}
	}
	if kind != "inventory" && (!f.From.Before(f.To) || f.To.Sub(f.From) > 93*24*time.Hour) {
		return false
	}
	return len(f.Movement) <= 40
}

func (s Service) owner(ctx context.Context, tx pgx.Tx, tenant, token, cap string) error {
	if _, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, token); err != nil {
		return err
	}
	return s.Entitlements.CheckWrite(ctx, tx, tenant, cap)
}

func (s Service) Read(ctx context.Context, token, kind string, f Filter) (any, error) {
	capability := map[string]string{"dashboard": "reports.dashboard", "sales": "reports.sales", "inventory": "reports.inventory", "movements": "reports.stock_movements", "transfers": "reports.stock_movements"}[kind]
	if capability == "" || !allowedInput(f, kind) {
		return nil, ErrInput
	}
	var result any
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if err := s.owner(ctx, tx, f.Tenant, token, capability); err != nil {
			return err
		}
		salesWindow := sales.ReportWindow{Tenant: f.Tenant, Outlet: f.Location, From: f.From, To: f.To}
		inv := inventory.ReportFilter{Tenant: f.Tenant, Location: f.Location, SKU: f.SKU, Actor: f.Actor, Kind: f.Movement, From: f.From, To: f.To}
		switch kind {
		case "dashboard":
			var d Dashboard
			var err error
			d.Total, d.Groups, err = sales.ProjectTotals(ctx, tx, salesWindow)
			if err != nil {
				return err
			}
			if err = tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&d.Updated); err != nil {
				return err
			}
			result = d
			return nil
		case "sales":
			k, err := cursor[sales.ReportSale](f, kind)
			if err != nil {
				return err
			}
			var at *time.Time
			if f.Cursor != "" {
				if k.At.IsZero() || !uuid.MatchString(k.ID) {
					return ErrInput
				}
				at = &k.At
			}
			rows, err := sales.ProjectSales(ctx, tx, salesWindow, at, k.ID)
			result = page(f, kind, rows, func(r sales.ReportSale) sales.ReportSale { return sales.ReportSale{At: r.At, ID: r.ID} })
			return err
		case "inventory":
			k, err := cursor[inventory.ReportBalance](f, kind)
			if err != nil {
				return err
			}
			if f.Cursor != "" && (!uuid.MatchString(k.Location) || !uuid.MatchString(k.SKU) || !uuid.MatchString(k.Batch)) {
				return ErrInput
			}
			rows, err := inventory.ProjectBalances(ctx, tx, inv, k)
			result = page(f, kind, rows, func(r inventory.ReportBalance) inventory.ReportBalance {
				return inventory.ReportBalance{Location: r.Location, SKU: r.SKU, Batch: r.Batch, Bucket: r.Bucket}
			})
			return err
		case "movements":
			k, err := cursor[inventory.ReportMovement](f, kind)
			if err != nil {
				return err
			}
			if f.Cursor != "" && (k.At.IsZero() || !uuid.MatchString(k.Document) || k.Line < 1 || k.Kind == "") {
				return ErrInput
			}
			rows, err := inventory.ProjectMovements(ctx, tx, inv, k)
			result = page(f, kind, rows, func(r inventory.ReportMovement) inventory.ReportMovement {
				return inventory.ReportMovement{At: r.At, Kind: r.Kind, Document: r.Document, Line: r.Line}
			})
			return err
		case "transfers":
			k, err := cursor[string](f, kind)
			if err != nil {
				return err
			}
			if f.Cursor != "" && !uuid.MatchString(k) {
				return ErrInput
			}
			rows, err := inventory.ProjectTransfers(ctx, tx, inv, k)
			result = page(f, kind, rows, func(r inventory.ReportTransfer) string { return r.ID })
			return err
		}
		return ErrInput
	})
	return result, err
}

func (s Service) Detail(ctx context.Context, tenant, token, outlet, id string) (sales.Sale, error) {
	var out sales.Sale
	if !uuid.MatchString(tenant) || !uuid.MatchString(outlet) || !uuid.MatchString(id) {
		return out, ErrInput
	}
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		if err = s.Entitlements.CheckWrite(ctx, tx, tenant, "reports.sales"); err != nil {
			return err
		}
		service := sales.Service{Authorize: func(_ context.Context, _ pgx.Tx, _ sales.Scope, action string) error {
			if action == sales.ReceiptPermission {
				return nil
			}
			return identity.ErrForbidden
		}}
		out, err = service.Detail(ctx, tx, sales.Scope{Tenant: tenant, Outlet: outlet, Actor: actor}, id, true)
		return err
	})
	return out, err
}

func (s Service) Capabilities(ctx context.Context, tenant, token string) ([]string, error) {
	var caps = []string{}
	if !uuid.MatchString(tenant) {
		return caps, ErrInput
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		for _, cap := range []string{"reports.dashboard", "reports.sales", "reports.inventory", "reports.stock_movements", "realtime.events", "realtime.notifications"} {
			err := s.Entitlements.CheckWrite(ctx, tx, tenant, cap)
			if errors.Is(err, entitlements.ErrDenied) {
				continue
			}
			if err != nil {
				return err
			}
			caps = append(caps, cap)
		}
		return nil
	})
	return caps, err
}
