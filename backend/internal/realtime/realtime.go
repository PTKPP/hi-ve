package realtime

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/identity"
	"hi-ve/backend/internal/platform/entitlements"
)

type Service struct {
	Push         PushConfig
	DB           *pgxpool.Pool
	Entitlements entitlements.Service
}

type Event struct {
	ID     string `json:"id"`
	Outlet string `json:"outlet_id"`
	Sale   string `json:"sale_id"`
	Kind   string `json:"kind"`
}

type Feed struct {
	Cursor        string  `json:"cursor"`
	Events        []Event `json:"events"`
	Notifications bool    `json:"sale_notifications"`
}

func (s Service) Feed(ctx context.Context, tenant, token string, after int64) (Feed, error) {
	f := Feed{Events: []Event{}, Cursor: strconv.FormatInt(after, 10)}
	err := pgx.BeginTxFunc(ctx, s.DB, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		if err = s.Entitlements.CheckWrite(ctx, tx, tenant, "realtime.events"); err != nil {
			return err
		}
		notificationErr := s.Entitlements.CheckWrite(ctx, tx, tenant, "realtime.notifications")
		if notificationErr != nil && !errors.Is(notificationErr, entitlements.ErrDenied) {
			return notificationErr
		}
		if notificationErr == nil {
			if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT sale_notifications FROM hive.realtime_preferences WHERE (tenant_id,actor_id)=($1,$2)),false)`, tenant, actor).Scan(&f.Notifications); err != nil {
				return err
			}
		}
		if after < 0 {
			return tx.QueryRow(ctx, `SELECT COALESCE((SELECT last_event FROM hive.realtime_streams WHERE tenant_id=$1),0)::text`, tenant).Scan(&f.Cursor)
		}
		rows, err := tx.Query(ctx, `SELECT sequence::text,outlet_id,sale_id,kind FROM hive.realtime_events
		    WHERE tenant_id=$1 AND sequence>$2 ORDER BY sequence LIMIT 100`, tenant, after)
		if err != nil {
			return err
		}
		f.Events, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Event])
		if len(f.Events) > 0 {
			f.Cursor = f.Events[len(f.Events)-1].ID
		}
		return err
	})
	return f, err
}

func (s Service) Preference(ctx context.Context, tenant, token string, value bool) error {
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := identity.AuthorizeTenantOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		if err = s.Entitlements.CheckWrite(ctx, tx, tenant, "realtime.notifications"); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO hive.realtime_preferences VALUES($1,$2,$3) ON CONFLICT(tenant_id,actor_id)
		    DO UPDATE SET sale_notifications=EXCLUDED.sale_notifications`, tenant, actor, value)
		return err
	})
}
