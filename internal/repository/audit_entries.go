package repository

import (
	"context"
	"fmt"

	"durpdeploy/internal/db"
)

// AuditLogEntry adds current identities and existing web targets to a record.
// The embedded record preserves the public API's original fields.
type AuditLogEntry struct {
	db.AuditLog
	ActorName  string `json:"actor_name"`
	ActorEmail string `json:"actor_email"`
	TargetURL  string `json:"target_url"`
}

func (r *Repository) ListAuditLogEntries(
	ctx context.Context,
	params db.ListAuditLogEntriesParams,
) ([]AuditLogEntry, error) {
	rows, err := r.Queries.ListAuditLogEntries(ctx, params)
	if err != nil {
		return nil, err
	}
	entries := make([]AuditLogEntry, len(rows))
	for i, row := range rows {
		entries[i] = AuditLogEntry{
			AuditLog: row.AuditLog, ActorName: row.ActorName,
			ActorEmail: row.ActorEmail, TargetURL: auditTargetURL(row),
		}
	}
	return entries, nil
}

func auditTargetURL(row db.ListAuditLogEntriesRow) string {
	// ponytail: omit legacy parent IDs; link these actions when audit records
	// carry a verified target ID and can distinguish older ambiguous records.
	switch row.AuditLog.Action {
	case "create_release", "refresh_release", "create_deployment",
		"create_runbook", "execute_runbook":
		return ""
	}
	if row.TargetID == 0 {
		return ""
	}
	switch row.AuditLog.EntityType {
	case "user":
		return fmt.Sprintf("/admin/users/%d/edit", row.TargetID)
	case "project":
		return fmt.Sprintf("/projects/%d", row.TargetID)
	case "environment":
		return fmt.Sprintf("/environments/%d/edit", row.TargetID)
	case "lifecycle":
		return fmt.Sprintf("/lifecycles/%d", row.TargetID)
	case "template":
		return fmt.Sprintf("/templates/%d/edit", row.TargetID)
	case "deployment":
		return fmt.Sprintf("/deployments/%d", row.TargetID)
	case "release":
		return fmt.Sprintf("/projects/%d/releases/%d",
			row.TargetProjectID, row.TargetID)
	case "runbook":
		return fmt.Sprintf("/projects/%d/runbooks/%d",
			row.TargetProjectID, row.TargetID)
	}
	return ""
}
