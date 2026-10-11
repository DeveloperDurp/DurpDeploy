package repository

import (
	"context"
	"database/sql"
	"log/slog"

	"durpdeploy/internal/db"
)

type queueAuditKey struct{}

// Audit after commit so an unavailable audit table cannot undo execution state.
func (r *Repository) withQueueTx(
	ctx context.Context, fn func(context.Context, *db.Queries) error,
) error {
	var entries []db.CreateAuditLogParams
	ctx = context.WithValue(ctx, queueAuditKey{}, &entries)
	if err := r.WithTx(ctx, func(q *db.Queries) error {
		if err := fn(ctx, q); err != nil {
			return err
		}
		return q.PurgeTerminalStaging(ctx)
	}); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := r.Queries.CreateAuditLog(ctx, entry); err != nil {
			slog.Warn("queue audit insert failed", "action", entry.Action,
				"err", err)
		}
	}
	return nil
}

func queueAudit(ctx context.Context, id int64, action string) {
	entries := ctx.Value(queueAuditKey{}).(*[]db.CreateAuditLogParams)
	*entries = append(*entries, db.CreateAuditLogParams{
		Action: action, EntityType: "deployment",
		EntityID: sql.NullInt64{Int64: id, Valid: true},
	})
}
