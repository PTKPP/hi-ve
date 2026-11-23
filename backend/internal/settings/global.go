package settings

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"hi-ve/backend/internal/audit"
	"hi-ve/backend/internal/identity"
)

const WebPushKey = "notifications.web_push_enabled"

var ErrGlobalInput = errors.New("invalid global setting")
var ErrGlobalConflict = errors.New("global setting conflict")

type Global struct{ DB *pgxpool.Pool }
type BooleanSetting struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}

func ReadWebPush(ctx context.Context, tx pgx.Tx) (BooleanSetting, error) {
	var value BooleanSetting
	err := tx.QueryRow(ctx, `SELECT enabled,revision FROM hive.global_boolean_settings WHERE key=$1 FOR SHARE`, WebPushKey).Scan(&value.Enabled, &value.Revision)
	return value, err
}
func (s Global) Get(ctx context.Context, token, key string) (BooleanSetting, error) {
	var out BooleanSetting
	if key != WebPushKey {
		return out, ErrGlobalInput
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := identity.AuthorizePlatform(ctx, tx, token, "platform.settings.read"); err != nil {
			return err
		}
		var err error
		out, err = ReadWebPush(ctx, tx)
		return err
	})
	return out, err
}
func (s Global) Put(ctx context.Context, token, key, request string, value bool, revision int64) (BooleanSetting, error) {
	var out BooleanSetting
	if key != WebPushKey || request == "" || revision < 0 {
		return out, ErrGlobalInput
	}
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := identity.AuthorizePlatform(ctx, tx, token, "platform.settings.update")
		if err != nil {
			return err
		}
		var old BooleanSetting
		err = tx.QueryRow(ctx, `SELECT enabled,revision FROM hive.global_boolean_settings WHERE key=$1 FOR UPDATE`, key).Scan(&old.Enabled, &old.Revision)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `SELECT enabled,revision FROM hive.global_setting_audit WHERE actor_id=$1 AND request_id=$2 AND key=$3`, actor, request, key).Scan(&out.Enabled, &out.Revision)
		if err == nil {
			if out.Enabled != value || out.Revision-1 != revision {
				return ErrGlobalConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if old.Revision != revision {
			return ErrGlobalConflict
		}
		err = tx.QueryRow(ctx, `UPDATE hive.global_boolean_settings SET enabled=$2,revision=revision+1 WHERE key=$1 RETURNING enabled,revision`, key, value).Scan(&out.Enabled, &out.Revision)
		if err != nil {
			return err
		}
		return audit.RecordGlobalBoolean(ctx, tx, actor, request, key, old.Enabled, value, out.Revision)
	})
	return out, err
}
