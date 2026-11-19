package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidCursor = errors.New("invalid login history cursor")

func RecordOwnerLogin(ctx context.Context, tx pgx.Tx, event, tenant, actor, outcome string) (bool, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO hive.online_login_history(event_id,tenant_id,actor_id,flow_class,outcome)
		VALUES ($1,$2,$3,'tenant_owner_google',$4) ON CONFLICT (event_id) DO NOTHING`, event, tenant, actor, outcome)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var same bool
	err = tx.QueryRow(ctx, `SELECT tenant_id=$2 AND actor_id=$3 AND flow_class='tenant_owner_google' AND outcome=$4
		FROM hive.online_login_history WHERE event_id=$1`, event, tenant, actor, outcome).Scan(&same)
	if err == nil && !same {
		err = errors.New("login event attributes conflict")
	}
	return false, err
}

type LoginHistory struct {
	DB             *pgxpool.Pool
	CursorKey      []byte
	AuthorizeOwner func(context.Context, pgx.Tx, string, string) (string, error)
}

type LoginFilter struct {
	Outcome, Actor, Cursor string
	From, To               *time.Time
	Limit                  int
}

type LoginEvent struct {
	ID         string    `json:"event_id"`
	Actor      string    `json:"actor_id"`
	Class      string    `json:"flow_class"`
	Outcome    string    `json:"outcome"`
	ReceivedAt time.Time `json:"received_at"`
}

type LoginPage struct {
	Events     []LoginEvent `json:"events"`
	NextCursor string       `json:"next_cursor"`
}

type loginPosition struct {
	Tenant string    `json:"tenant"`
	Event  string    `json:"event"`
	Time   time.Time `json:"time"`
}

func (s LoginHistory) mac(payload string) []byte {
	mac := hmac.New(sha256.New, s.CursorKey)
	mac.Write([]byte("hive.login-history.cursor.v1\x00" + payload))
	return mac.Sum(nil)
}

func (s LoginHistory) cursor(tenant string, last LoginEvent) (string, error) {
	data, err := json.Marshal(loginPosition{Tenant: tenant, Event: last.ID, Time: last.ReceivedAt})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(s.mac(encoded)), nil
}

func (s LoginHistory) position(tenant, cursor string) (loginPosition, error) {
	var position loginPosition
	body, signature, found := strings.Cut(cursor, ".")
	if !found || len(cursor) > 512 {
		return position, ErrInvalidCursor
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil || !hmac.Equal(sig, s.mac(body)) {
		return position, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil || json.Unmarshal(data, &position) != nil || position.Tenant != tenant || position.Event == "" || position.Time.IsZero() {
		return loginPosition{}, ErrInvalidCursor
	}
	return position, nil
}

func (s LoginHistory) List(ctx context.Context, tenant, token string, filter LoginFilter) (LoginPage, error) {
	page := LoginPage{Events: []LoginEvent{}}
	if filter.Limit < 1 || filter.Limit > 100 || s.DB == nil || len(s.CursorKey) < 32 {
		return page, errors.New("login history unavailable")
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		var position loginPosition
		var before *time.Time
		if filter.Cursor != "" {
			var err error
			position, err = s.position(tenant, filter.Cursor)
			if err != nil {
				return err
			}
			before = &position.Time
		}
		rows, err := tx.Query(ctx, `SELECT event_id,actor_id,flow_class,outcome,received_at
			FROM hive.online_login_history
			WHERE tenant_id=$1 AND received_at>statement_timestamp()-interval '168 hours'
				AND ($2='' OR outcome=$2) AND ($3='' OR actor_id=NULLIF($3,'')::uuid)
				AND ($4::timestamptz IS NULL OR received_at >= $4)
				AND ($5::timestamptz IS NULL OR received_at < $5)
				AND ($6::timestamptz IS NULL OR (received_at,event_id)<($6,NULLIF($7,'')::uuid))
			ORDER BY received_at DESC,event_id DESC LIMIT $8`, tenant, filter.Outcome, filter.Actor, filter.From,
			filter.To, before, position.Event, filter.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var event LoginEvent
			if err = rows.Scan(&event.ID, &event.Actor, &event.Class, &event.Outcome, &event.ReceivedAt); err != nil {
				return err
			}
			event.ReceivedAt = event.ReceivedAt.UTC()
			page.Events = append(page.Events, event)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(page.Events) > filter.Limit {
			page.Events = page.Events[:filter.Limit]
			page.NextCursor, err = s.cursor(tenant, page.Events[len(page.Events)-1])
		}
		return err
	})
	return page, err
}

// Cleanup deletes one bounded batch. A full batch signals possible backlog.
func (s LoginHistory) Cleanup(ctx context.Context) (bool, error) {
	tag, err := s.DB.Exec(ctx, `WITH expired AS (
		SELECT event_id FROM hive.online_login_history WHERE received_at<=statement_timestamp()-interval '168 hours'
		ORDER BY received_at,event_id LIMIT 500 FOR UPDATE SKIP LOCKED
	) DELETE FROM hive.online_login_history l USING expired e WHERE l.event_id=e.event_id`)
	return tag.RowsAffected() == 500, err
}
