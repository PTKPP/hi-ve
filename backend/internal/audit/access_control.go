package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const AccessControlCategory = "access_control"

type AccessControlReader struct {
	DB                *pgxpool.Pool
	CursorKey         []byte
	AuthorizeOwner    func(context.Context, pgx.Tx, string, string) (string, error)
	TenantLocationIDs func(context.Context, pgx.Tx, string, []string) ([]string, error)
}

type AccessControlEvent struct {
	ID         string    `json:"event_id"`
	Actor      string    `json:"actor_id"`
	Target     string    `json:"target_id"`
	Action     string    `json:"action"`
	Revision   int64     `json:"revision"`
	OccurredAt time.Time `json:"occurred_at"`
	Locations  []string  `json:"location_ids"`
}

type AccessControlPage struct {
	Events     []AccessControlEvent `json:"events"`
	NextCursor string               `json:"next_cursor"`
}

func (s AccessControlReader) sign(tenant, actor, revision string) []byte {
	mac := hmac.New(sha256.New, s.CursorKey)
	mac.Write([]byte("hive.audit.access_control.membership.location_roles.replace.v1\x00" + tenant + "\x00" + actor + "\x00" + revision))
	return mac.Sum(nil)
}

func (s AccessControlReader) beforeRevision(tenant, actor, cursor string) (*int64, error) {
	if cursor == "" {
		return nil, nil
	}
	value, signature, ok := strings.Cut(cursor, ".")
	if !ok || len(cursor) > 80 {
		return nil, ErrInvalidCursor
	}
	tag, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil || !hmac.Equal(tag, s.sign(tenant, actor, value)) {
		return nil, ErrInvalidCursor
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision <= 0 {
		return nil, ErrInvalidCursor
	}
	return &revision, nil
}

// Track the difference as role/location pairs so a role change within one
// location is still an affected location and unchanged assignments disappear.
func affectedLocations(before, after []byte) ([]string, error) {
	type assignment struct {
		Role     pgtype.UUID `json:"role_id"`
		Location pgtype.UUID `json:"location_id"`
	}
	var previous, current []assignment
	if err := json.Unmarshal(before, &previous); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(after, &current); err != nil {
		return nil, err
	}
	added := make(map[assignment]struct{}, len(current))
	for _, item := range current {
		if !item.Role.Valid || !item.Location.Valid {
			return nil, errors.New("invalid audit assignment")
		}
		added[item] = struct{}{}
	}
	ids := []string{}
	for _, item := range previous {
		if !item.Role.Valid || !item.Location.Valid {
			return nil, errors.New("invalid audit assignment")
		}
		if _, same := added[item]; same {
			delete(added, item)
			continue
		}
		ids = append(ids, item.Location.String())
	}
	for item := range added {
		ids = append(ids, item.Location.String())
	}
	return ids, nil
}

func (s AccessControlReader) List(ctx context.Context, tenant, token, cursor string, limit int) (AccessControlPage, error) {
	page := AccessControlPage{Events: []AccessControlEvent{}}
	if s.DB == nil || len(s.CursorKey) < 32 || s.AuthorizeOwner == nil || s.TenantLocationIDs == nil || limit < 1 || limit > 100 {
		return page, errors.New("access-control audit reader unavailable")
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := s.AuthorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		beforeRevision, err := s.beforeRevision(tenant, actor, cursor)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,actor_id,target_id,action,revision,occurred_at,before_state,after_state
			FROM hive.tenant_rbac_audit WHERE tenant_id=$1 AND action='membership.location_roles.replace'
			AND revision<=COALESCE($2::bigint-1,9223372036854775807)
			ORDER BY revision DESC LIMIT $3`, tenant, beforeRevision, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var event AccessControlEvent
			var before, after []byte
			if err = rows.Scan(&event.ID, &event.Actor, &event.Target, &event.Action, &event.Revision, &event.OccurredAt, &before, &after); err != nil {
				return err
			}
			event.OccurredAt = event.OccurredAt.UTC()
			event.Locations, err = affectedLocations(before, after)
			if err != nil {
				return err
			}
			page.Events = append(page.Events, event)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(page.Events) > limit {
			page.Events = page.Events[:limit]
			revision := strconv.FormatInt(page.Events[limit-1].Revision, 10)
			page.NextCursor = revision + "." + base64.RawURLEncoding.EncodeToString(s.sign(tenant, actor, revision))
		}
		var all []string
		for _, event := range page.Events {
			all = append(all, event.Locations...)
		}
		if len(all) == 0 {
			return nil
		}
		known, err := s.TenantLocationIDs(ctx, tx, tenant, all)
		if err != nil {
			return err
		}
		allowed := make(map[string]bool, len(known))
		for _, id := range known {
			allowed[id] = true
		}
		for i := range page.Events {
			ids := page.Events[i].Locations
			slices.Sort(ids)
			page.Events[i].Locations = slices.DeleteFunc(slices.Compact(ids), func(id string) bool { return !allowed[id] })
		}
		return nil
	})
	return page, err
}
