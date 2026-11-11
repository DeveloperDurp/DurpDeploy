package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

var ErrQueueRecoveryBlocked = errors.New(
	"deployment queue has unresolved execution, cleanup, or buffered logs",
)

// QueueRecovery describes persisted evidence, never an assertion of cleanup.
type QueueRecovery struct {
	DeploymentID     int64                              `json:"deployment_id"`
	Status           string                             `json:"status"`
	BlockerIDs       []int64                            `json:"blocker_ids"`
	LocalNamespace   bool                               `json:"local_namespace"`
	CleanupConfirmed bool                               `json:"cleanup_confirmed"`
	CleanupEvidence  bool                               `json:"cleanup_evidence"`
	BufferedLogs     int64                              `json:"buffered_remote_logs"`
	RemoteRuns       []db.ListDeploymentRecoveryRunsRow `json:"remote_runs"`
}

func (r *Repository) DeploymentQueueDiagnostics(
	ctx context.Context,
) ([]QueueRecovery, error) {
	result := []QueueRecovery{}
	err := r.WithTx(ctx, func(q *db.Queries) error {
		ids, err := q.ListDeploymentQueueSlots(ctx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			state, err := deploymentQueueRecovery(ctx, q, id)
			if err != nil {
				return err
			}
			result = append(result, state)
		}
		return nil
	})
	return result, err
}

func deploymentQueueRecovery(
	ctx context.Context,
	q *db.Queries,
	id int64,
) (QueueRecovery, error) {
	d, err := q.GetDeployment(ctx, id)
	if err != nil {
		return QueueRecovery{}, err
	}
	blockers, err := q.ListDeploymentQueueBlockers(ctx, id)
	if err != nil {
		return QueueRecovery{}, err
	}
	buffered, err := q.CountDeploymentBufferedRemoteLogs(ctx, id)
	if err != nil {
		return QueueRecovery{}, err
	}
	runs, err := q.ListDeploymentRecoveryRuns(ctx, id)
	if err != nil {
		return QueueRecovery{}, err
	}
	if blockers == nil {
		blockers = []int64{}
	}
	if runs == nil {
		runs = []db.ListDeploymentRecoveryRunsRow{}
	}
	return QueueRecovery{
		DeploymentID: id, Status: d.Status, BlockerIDs: blockers,
		LocalNamespace:   d.ContainerNamespace.Valid,
		CleanupConfirmed: d.CleanupConfirmedAt.Valid,
		CleanupEvidence:  queueCleanupEvidence(d, runs),
		BufferedLogs:     buffered, RemoteRuns: runs,
	}, nil
}

func queueCleanupEvidence(
	d db.Deployment,
	runs []db.ListDeploymentRecoveryRunsRow,
) bool {
	if d.CleanupConfirmedAt.Valid {
		return true
	}
	if d.ContainerNamespace.Valid {
		return false
	}
	for _, run := range runs {
		if run.CleanupConfirmedAt.Valid {
			return true
		}
	}
	return false
}

// ReconcileDeploymentQueue only releases an existing slot with no blockers.
// It does not create cleanup evidence or change a terminal outcome.
func (r *Repository) ReconcileDeploymentQueue(
	ctx context.Context,
	id int64,
	entry db.CreateAuditLogParams,
) (bool, error) {
	changed := false
	err := withSQLiteBusyRetry(ctx, func() error {
		changed = false
		d, err := r.Queries.GetDeployment(ctx, id)
		if err != nil {
			return err
		}
		return r.withQueueTx(
			ctx,
			func(ctx context.Context, q *db.Queries) error {
				if _, err := q.LockDeploymentEnvironment(
					ctx,
					d.EnvironmentID,
				); err != nil {
					return err
				}
				owner, err := q.GetDeploymentSlot(ctx, id)
				switch {
				case errors.Is(err, sql.ErrNoRows):
					return nil
				case err != nil:
					return err
				case owner != id:
					return nil
				}
				state, err := deploymentQueueRecovery(ctx, q, id)
				if err != nil {
					return err
				}
				// Missing protocol rows are not proof that deleted work stopped.
				if len(state.BlockerIDs) != 0 || state.BufferedLogs != 0 ||
					!state.CleanupEvidence {
					return ErrQueueRecoveryBlocked
				}
				if err := advanceDeploymentQueue(ctx, q, id); err != nil {
					return err
				}
				entry.Action = "reconcile_deployment_queue"
				entry.EntityType = "deployment"
				entry.EntityID = sql.NullInt64{Int64: id, Valid: true}
				if _, err := q.CreateAuditLog(ctx, entry); err != nil {
					return err
				}
				changed = true
				return nil
			},
		)
	})
	return changed && err == nil, err
}
