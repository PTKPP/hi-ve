package settings

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
)

var (
	ErrInput    = errors.New("invalid receipt settings")
	ErrConflict = errors.New("receipt settings conflict")
)

type Service struct {
	DB             *pgxpool.Pool
	FingerprintKey []byte
	AuthorizeOwner func(context.Context, pgx.Tx, string, string) (string, error)
}

type Receipt struct {
	Footer   *string `json:"receipt_footer"`
	Revision int64   `json:"revision"`
}

type ReceiptUpdate struct {
	Footer   json.RawMessage `json:"receipt_footer"`
	Expected *int64          `json:"expected_revision"`
}

func (s Service) Receipt(ctx context.Context, tenant, token string) (Receipt, error) {
	var result Receipt
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := s.AuthorizeOwner(ctx, tx, tenant, token); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT receipt_footer,revision FROM hive.tenant_receipt_settings WHERE tenant_id=$1`, tenant).
			Scan(&result.Footer, &result.Revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	return result, err
}

func (s Service) ReplaceReceipt(ctx context.Context, tenant, token, key string, input ReceiptUpdate) (Receipt, error) {
	var result Receipt
	if input.Expected == nil || *input.Expected < 0 || len(input.Footer) == 0 || json.Unmarshal(input.Footer, &result.Footer) != nil {
		return result, ErrInput
	}
	if result.Footer != nil {
		if strings.ContainsFunc(*result.Footer, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) {
			return Receipt{}, ErrInput
		}
		trimmed := strings.TrimSpace(*result.Footer)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > 160 {
			return Receipt{}, ErrInput
		}
		result.Footer = &trimmed
	}
	payload, err := json.Marshal(Receipt{Footer: result.Footer, Revision: *input.Expected})
	if err != nil {
		return Receipt{}, err
	}
	mac := hmac.New(sha256.New, s.FingerprintKey)
	mac.Write([]byte("receipt-settings\x00"))
	mac.Write(payload)
	fingerprint := mac.Sum(nil)
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := s.AuthorizeOwner(ctx, tx, tenant, token)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO hive.receipt_settings_receipts(tenant_id,actor_id,request_key,fingerprint)
			VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, actor, key, fingerprint)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var previous []byte
			err = tx.QueryRow(ctx, `SELECT fingerprint,result FROM hive.receipt_settings_receipts
				WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key).Scan(&previous, &result)
			if err == nil && !hmac.Equal(previous, fingerprint) {
				return ErrConflict
			}
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO hive.tenant_receipt_settings(tenant_id) VALUES($1) ON CONFLICT DO NOTHING`, tenant); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `UPDATE hive.tenant_receipt_settings SET receipt_footer=$2,revision=revision+1
			WHERE tenant_id=$1 AND revision=$3 RETURNING revision`, tenant, result.Footer, input.Expected).Scan(&result.Revision)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if err = audit.RecordReceiptSettings(ctx, tx, tenant, actor, result.Revision); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE hive.receipt_settings_receipts SET result=$4
			WHERE tenant_id=$1 AND actor_id=$2 AND request_key=$3`, tenant, actor, key, result)
		return err
	})
	return result, err
}

// ReceiptFooter snapshots tenant-owned receipt settings in a business transaction.
func ReceiptFooter(ctx context.Context, tx pgx.Tx, tenant string) (*string, error) {
	var footer *string
	err := tx.QueryRow(ctx, `SELECT receipt_footer FROM hive.tenant_receipt_settings WHERE tenant_id=$1`, tenant).Scan(&footer)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return footer, err
}
