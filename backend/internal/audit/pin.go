package audit

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func RecordStaffPIN(ctx context.Context, tx pgx.Tx, tenant, actor, member, action string, revision int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.staff_pin_management_events
        (tenant_id,actor_id,member_id,action,revision) VALUES ($1,$2,$3,$4,$5)`,
		tenant, actor, member, action, revision)
	return err
}

func RecordOperatorLogin(ctx context.Context, tx pgx.Tx, tenant, attempt, member, device, outlet, outcome string) error {
	_, err := tx.Exec(ctx, `INSERT INTO hive.operator_login_history(tenant_id,attempt_id,member_id,device_id,outlet_id,outcome)
      VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (tenant_id,attempt_id,outcome) DO NOTHING`,
		tenant, attempt, member, device, outlet, outcome)
	return err
}

type OperatorFilter struct {
	LoginFilter
	Device, Outlet string
}
type OperatorEvent struct {
	ID         string    `json:"event_id"`
	Member     string    `json:"member_id"`
	Device     string    `json:"device_id"`
	Outlet     string    `json:"outlet_id"`
	Outcome    string    `json:"outcome"`
	OccurredAt time.Time `json:"occurred_at"`
}
type OperatorPage struct {
	Events []OperatorEvent `json:"events"`
	Next   string          `json:"next_cursor"`
}

func (s LoginHistory) ListOperators(ctx context.Context, tenant, token string, f OperatorFilter) (OperatorPage, error) {
	page := OperatorPage{Events: []OperatorEvent{}}
	if f.Limit < 1 || f.Limit > 100 || s.DB == nil || len(s.CursorKey) < 32 {
		return page, errors.New("operator login history unavailable")
	}
	scope := "operator:" + tenant
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		var at *time.Time
		var id string
		if f.Cursor != "" {
			p, err := s.position(scope, f.Cursor)
			if err != nil {
				return err
			}
			at, id = &p.Time, p.Event
		}
		rows, err := tx.Query(ctx, `SELECT event_id,member_id,device_id,outlet_id,outcome,occurred_at
          FROM hive.operator_login_history WHERE tenant_id=$1 AND occurred_at>statement_timestamp()-interval '168 hours'
          AND ($2='' OR outcome=$2) AND ($3='' OR member_id=NULLIF($3,'')::uuid)
          AND ($4='' OR device_id=NULLIF($4,'')::uuid) AND ($5='' OR outlet_id=NULLIF($5,'')::uuid)
          AND ($6::timestamptz IS NULL OR occurred_at >= $6) AND ($7::timestamptz IS NULL OR occurred_at<$7)
          AND ($8::timestamptz IS NULL OR (occurred_at,event_id)<($8,NULLIF($9,'')::uuid))
          ORDER BY occurred_at DESC,event_id DESC LIMIT $10`, tenant, f.Outcome, f.Actor, f.Device, f.Outlet, f.From, f.To, at, id, f.Limit+1)
		if err != nil {
			return err
		}
		events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[OperatorEvent])
		if err != nil {
			return err
		}
		if len(events) > f.Limit {
			events = events[:f.Limit]
			last := events[f.Limit-1]
			page.Next, err = s.cursor(scope, LoginEvent{ID: last.ID, ReceivedAt: last.OccurredAt})
			if err != nil {
				return err
			}
		}
		for i := range events {
			events[i].OccurredAt = events[i].OccurredAt.UTC()
		}
		page.Events = append(page.Events, events...)
		return nil
	})
	return page, err
}

func (s LoginHistory) CleanupOperators(ctx context.Context) (bool, error) {
	tag, err := s.DB.Exec(ctx, `WITH expired AS (
      SELECT event_id FROM hive.operator_login_history WHERE occurred_at<=statement_timestamp()-interval '168 hours'
      ORDER BY occurred_at,event_id LIMIT 500 FOR UPDATE SKIP LOCKED
    ) DELETE FROM hive.operator_login_history l USING expired e WHERE l.event_id=e.event_id`)
	return tag.RowsAffected() == 500, err
}
