package repository

import (
	"context"
	"database/sql"
	"errors"
	"io"

	"durpdeploy/internal/db"
)

var ErrEnvironmentReserved = errors.New(
	"environment is reserved by an active artifact-gated deployment",
)

func gateAdmission(
	ctx context.Context,
	q *db.Queries,
	environmentID, deploymentID int64,
	gated bool,
) error {
	if _, err := q.LockGateEnvironment(ctx, environmentID); err != nil {
		return err
	}
	var flag int64
	if gated {
		flag = 1
	}
	count, err := q.CountGateEnvironmentConflicts(
		ctx,
		db.CountGateEnvironmentConflictsParams{
			EnvironmentID: environmentID,
			DeploymentID:  deploymentID,
			Gated:         flag,
		},
	)
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrEnvironmentReserved
	}
	return nil
}

func (r *Repository) BeginArtifactGateRun(
	ctx context.Context,
	deploymentID int64,
) (db.ArtifactGateRun, bool, error) {
	run, err := r.Queries.GetArtifactGateRun(ctx, deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return run, false, nil
	}
	if err != nil {
		return run, true, err
	}
	err = r.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockArtifactGateDeployment(
			ctx,
			deploymentID,
		); err != nil {
			return err
		}
		run, err = q.GetArtifactGateRun(ctx, deploymentID)
		if err != nil {
			return err
		}
		n, err := q.ClaimArtifactGateRun(ctx, deploymentID)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrArtifactGate
		}
		deployment, err := q.GetDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		if !deployment.StartedAt.Valid {
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			deployment.StartedAt = sql.NullInt64{Int64: now, Valid: true}
		}
		return q.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:        deploymentID,
				Status:    "running",
				StartedAt: deployment.StartedAt,
			},
		)
	})
	return run, true, err
}

func (r *Repository) PauseArtifactGate(
	ctx context.Context,
	deploymentID, nextStep int64,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		n, err := q.PauseArtifactGateDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrArtifactGate
		}
		return q.AdvanceArtifactGateRun(
			ctx,
			db.AdvanceArtifactGateRunParams{
				DeploymentID: deploymentID,
				NextStep:     nextStep,
			},
		)
	})
}

func (r *Repository) ApproveArtifact(
	ctx context.Context,
	deploymentID, stepIndex, revision, userID int64,
	checksum string,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.LockArtifactGateDeployment(
				ctx,
				deploymentID,
			); err != nil {
				return err
			}
			deployment, err := q.GetDeployment(ctx, deploymentID)
			if err != nil {
				return err
			}
			if deployment.Status != "awaiting_artifact_approval" {
				return ErrArtifactGate
			}
			gate, err := q.GetArtifactGate(
				ctx,
				db.GetArtifactGateParams{
					DeploymentID: deploymentID,
					StepIndex:    stepIndex,
				},
			)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrArtifactGate
			}
			if err != nil {
				return err
			}
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			if gate.Status != "awaiting" || gate.Revision != revision ||
				gate.ArtifactSha256 != checksum ||
				gate.ExpiresAt <= now {
				return ErrArtifactGate
			}
			if err := r.WriteArtifactGateBundle(
				ctx,
				q,
				gate,
				io.Discard,
			); err != nil {
				return err
			}
			n, err := q.ApproveArtifactGate(ctx, db.ApproveArtifactGateParams{
				DeploymentID:   deploymentID,
				StepIndex:      stepIndex,
				Revision:       revision,
				ArtifactSha256: checksum,
				Now:            now,
				ApprovedBy:     sql.NullInt64{Int64: userID, Valid: true},
				ApprovedAt:     sql.NullInt64{Int64: now, Valid: true},
			})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrArtifactGate
			}
			n, err = q.ResumeArtifactGateDeployment(ctx, deploymentID)
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrArtifactGate
			}
			return nil
		})
	})
}

func (r *Repository) RejectArtifact(
	ctx context.Context,
	deploymentID int64,
	status string,
) error {
	return r.rejectArtifact(ctx, deploymentID, status, nil)
}

func (r *Repository) RejectArtifactGate(
	ctx context.Context,
	deploymentID, stepIndex, revision int64,
	checksum string,
) error {
	return r.rejectArtifact(
		ctx,
		deploymentID,
		"rejected",
		func(q *db.Queries) error {
			gate, err := q.GetArtifactGate(
				ctx,
				db.GetArtifactGateParams{
					DeploymentID: deploymentID,
					StepIndex:    stepIndex,
				},
			)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrArtifactGate
			}
			if err != nil {
				return err
			}
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			if gate.Status != "awaiting" || gate.ExpiresAt <= now ||
				gate.Revision != revision ||
				gate.ArtifactSha256 != checksum {
				return ErrArtifactGate
			}
			return nil
		},
	)
}

func (r *Repository) rejectArtifact(
	ctx context.Context,
	deploymentID int64,
	status string,
	validate func(*db.Queries) error,
) error {
	if status != "rejected" && status != "cancelled" {
		return ErrArtifactGate
	}
	return r.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockArtifactGateDeployment(
			ctx,
			deploymentID,
		); err != nil {
			return err
		}
		deployment, err := q.GetDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		if deployment.Status != "awaiting_artifact_approval" {
			return ErrArtifactGate
		}
		if validate != nil {
			if err := validate(q); err != nil {
				return err
			}
		}
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		if err := q.FinishArtifactGates(
			ctx,
			db.FinishArtifactGatesParams{
				DeploymentID: deploymentID,
				Status:       status,
			},
		); err != nil {
			return err
		}
		if err := q.UpdateDeploymentStatus(ctx, db.UpdateDeploymentStatusParams{
			ID:         deploymentID,
			Status:     status,
			FinishedAt: sql.NullInt64{Int64: now, Valid: true},
		}); err != nil {
			return err
		}
		return q.ReconcileTerminalVerifications(ctx)
	})
}

func (r *Repository) MaintainArtifactGates(ctx context.Context) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			if err := q.ExpireArtifactGateDeployments(
				ctx,
				sql.NullInt64{Int64: now, Valid: true},
			); err != nil {
				return err
			}
			if err := q.ExpireArtifactGates(ctx, now); err != nil {
				return err
			}
			if err := q.ReconcileTerminalVerifications(ctx); err != nil {
				return err
			}
			if err := q.CancelTerminalArtifactGates(ctx); err != nil {
				return err
			}
			return q.DeleteExpiredArtifactGateChunks(
				ctx,
				sql.NullInt64{Int64: now - 7*24*60*60, Valid: true},
			)
		})
	})
}
