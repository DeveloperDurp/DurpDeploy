package repository

import (
	"context"
	"database/sql"
	"log/slog"

	"durpdeploy/internal/db"
)

const (
	RecoveryCleanupConfirmed   = "Reconciled after server restart: local container cleanup confirmed."
	RecoveryCleanupUnconfirmed = "Reconciled after server restart: local container cleanup remains unconfirmed."
	RecoveryOrphanFailed       = "Reconciled after server restart: interrupted execution failed."
)

// RecordStartupRecovery runs in the transaction that changes recovery state.
func RecordStartupRecovery(
	ctx context.Context,
	q *db.Queries,
	deployments []db.Deployment,
	reason string,
) error {
	for _, deployment := range deployments {
		note := reason
		if deployment.Note.Valid && deployment.Note.String != "" {
			note = deployment.Note.String + "\n" + reason
		}
		if err := q.UpdateDeploymentRecoveryNote(ctx,
			db.UpdateDeploymentRecoveryNoteParams{
				ID:   deployment.ID,
				Note: sql.NullString{String: note, Valid: true},
			}); err != nil {
			return err
		}
		if _, err := q.CreateDeploymentLog(ctx, db.CreateDeploymentLogParams{
			DeploymentID: deployment.ID, Line: reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

// WarnStartupRecovery must run only after the recovery transaction commits.
func WarnStartupRecovery(deployments []db.Deployment, reason string) {
	for _, deployment := range deployments {
		slog.Warn("startup recovery: reconciled deployment",
			"deployment_id", deployment.ID, "status", deployment.Status,
			"reason", reason)
	}
}
