package audit

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CriticalAuditTables lists the tables governed by OPEN-006. The database repeats this allowlist.
var CriticalAuditTables = []string{
	"provisioning_audit", "tenant_rbac_audit", "entitlement_audit", "membership_lifecycle_audit",
	"location_create_audit", "session_logout_audit", "device_pairing_audit", "operational_device_audit",
	"receipt_settings_audit", "staff_pin_management_events",
}

type RetentionResult struct {
	Table   string
	Outcome string
	Reason  string
	Rows    int64
}

// Retention calls the fail-closed purge function with a dedicated retention role.
// The API runtime role has no EXECUTE privilege and never uses this type.
type Retention struct {
	DB *pgxpool.Pool
}

// RunBatch runs one statement, so the delete and its ledger row commit together.
// Rows is the deleted count when apply is true, otherwise the eligible count.
func (s Retention) RunBatch(ctx context.Context, table string, batch int, apply bool) (RetentionResult, error) {
	result := RetentionResult{Table: table}
	if s.DB == nil {
		return result, errors.New("retention database unavailable")
	}
	err := s.DB.QueryRow(ctx, `SELECT r_outcome,r_reason,r_rows FROM hive.purge_critical_audit($1,$2,$3)`,
		table, batch, apply).Scan(&result.Outcome, &result.Reason, &result.Rows)
	return result, err
}
