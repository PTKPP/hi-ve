package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hi-ve/backend/internal/audit"
)

const maxBatchesPerTable = 200

// One-shot job for an external scheduler. It never runs inside the API process.
// Exit 1 reports an error and exit 3 reports a fail-closed refusal; neither deletes unsafely.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	refused, err := run(ctx)
	switch {
	case err != nil:
		slog.Error("audit retention stopped", "reason", err.Error())
		os.Exit(1)
	case refused:
		slog.Warn("audit retention refused at least one batch")
		os.Exit(3)
	}
}

func run(ctx context.Context) (bool, error) {
	dsn := os.Getenv("HIVE_AUDIT_RETENTION_DATABASE_URL")
	if dsn == "" {
		return false, errors.New("retention database is not configured")
	}
	batch := 500
	if raw := os.Getenv("HIVE_AUDIT_RETENTION_BATCH"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000 {
			return false, errors.New("invalid retention batch size")
		}
		batch = value
	}
	// Dry run is the default. Only the exact value true applies deletions.
	apply := os.Getenv("HIVE_AUDIT_RETENTION_APPLY") == "true"
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return false, errors.New("invalid retention database configuration")
	}
	defer db.Close()
	if db.Ping(ctx) != nil {
		return false, errors.New("retention database unavailable")
	}
	service := audit.Retention{DB: db}
	refused := false
	for _, table := range audit.CriticalAuditTables {
		for range maxBatchesPerTable {
			result, err := service.RunBatch(ctx, table, batch, apply)
			if err != nil {
				return refused, errors.New("retention batch failed")
			}
			slog.Info("audit retention batch", "table", result.Table, "apply", apply,
				"outcome", result.Outcome, "reason", result.Reason, "rows", result.Rows)
			if result.Outcome != "ok" {
				refused = true
				break
			}
			if !apply || result.Rows < int64(batch) {
				break
			}
		}
	}
	return refused, nil
}
