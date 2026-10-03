package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
)

var (
	ErrRollbackConflict = errors.New(
		"rollback requires the latest terminal deployment; reload the confirmation",
	)
	ErrRollbackTargetChanged = errors.New(
		"rollback target changed; reload the confirmation")
	ErrNoRollbackTarget = errors.New("no prior successful different release")
)

// swagger:model RollbackPreview
type RollbackPreview struct {
	SourceDeploymentID int64  `json:"source_deployment_id"`
	TargetDeploymentID int64  `json:"target_deployment_id"`
	SourceReleaseID    int64  `json:"source_release_id"`
	TargetReleaseID    int64  `json:"target_release_id"`
	SourceVersion      string `json:"source_version"`
	TargetVersion      string `json:"target_version"`
	ProjectID          int64  `json:"project_id"`
	EnvironmentID      int64  `json:"environment_id"`
	EnvironmentName    string `json:"environment_name"`
	RequiresApproval   bool   `json:"requires_approval"`
	Blocked            bool   `json:"blocked"`
	Reason             string `json:"reason,omitempty"`
}

func (r *Repository) PreviewRollback(
	ctx context.Context, sourceID int64,
) (RollbackPreview, error) {
	return previewRollback(ctx, r.Queries, sourceID)
}

func previewRollback(
	ctx context.Context, q *db.Queries, sourceID int64,
) (RollbackPreview, error) {
	source, err := q.GetDeployment(ctx, sourceID)
	if err != nil {
		return RollbackPreview{}, err
	}
	if source.Kind != "deployment" || (source.Status != "succeeded" &&
		source.Status != "failed" && source.Status != "cancelled") {
		return RollbackPreview{}, ErrRollbackConflict
	}
	release, err := q.GetDeploymentRelease(ctx, source.ReleaseID)
	if err != nil {
		return RollbackPreview{}, err
	}
	latest, err := q.GetLatestProjectEnvironmentDeployment(ctx,
		db.GetLatestProjectEnvironmentDeploymentParams{
			ProjectID: release.ProjectID, EnvironmentID: source.EnvironmentID,
		})
	if err != nil {
		return RollbackPreview{}, err
	}
	if latest.ID != source.ID {
		return RollbackPreview{}, ErrRollbackConflict
	}
	active, err := q.HasActiveProjectEnvironmentDeployment(ctx,
		db.HasActiveProjectEnvironmentDeploymentParams{
			ProjectID: release.ProjectID, EnvironmentID: source.EnvironmentID,
		})
	if err != nil {
		return RollbackPreview{}, err
	}
	if active != 0 {
		return RollbackPreview{}, ErrRollbackConflict
	}
	target, err := q.GetRollbackTarget(ctx, sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return RollbackPreview{}, ErrNoRollbackTarget
	}
	if err != nil {
		return RollbackPreview{}, err
	}
	targetRelease, err := q.GetDeploymentRelease(ctx, target.ReleaseID)
	if err != nil {
		return RollbackPreview{}, err
	}
	project, err := q.GetProject(ctx, release.ProjectID)
	if err != nil {
		return RollbackPreview{}, err
	}
	env, err := q.GetEnvironment(ctx, source.EnvironmentID)
	if err != nil {
		return RollbackPreview{}, err
	}
	blocked, reason, approval, err := gate.CheckAndApproval(ctx, q,
		project, targetRelease, source.EnvironmentID)
	if err != nil {
		return RollbackPreview{}, err
	}
	return RollbackPreview{
		SourceDeploymentID: source.ID, TargetDeploymentID: target.ID,
		SourceReleaseID: release.ID, TargetReleaseID: targetRelease.ID,
		SourceVersion: release.Version, TargetVersion: targetRelease.Version,
		ProjectID: project.ID, EnvironmentID: env.ID,
		EnvironmentName: env.Name, Blocked: blocked,
		Reason: reason, RequiresApproval: approval,
	}, nil
}

func (r *Repository) CreateRollback(
	ctx context.Context, sourceID, expectedTargetID int64,
) (DeploymentResult, error) {
	var result DeploymentResult
	err := withSQLiteBusyRetry(ctx, func() error {
		result = DeploymentResult{}
		return r.WithTx(ctx, func(q *db.Queries) error {
			source, err := q.GetDeployment(ctx, sourceID)
			if err != nil {
				return err
			}
			if err := lockRelease(ctx, q, source.ReleaseID); err != nil {
				return err
			}
			preview, err := previewRollback(ctx, q, sourceID)
			if err != nil {
				return err
			}
			if expectedTargetID != preview.TargetDeploymentID {
				return ErrRollbackTargetChanged
			}
			if preview.Blocked {
				return &RollbackGateError{Reason: preview.Reason}
			}
			status := "pending"
			if preview.RequiresApproval {
				status = "pending_approval"
			}
			result, err = createDeploymentFromDeployment(ctx, q,
				db.CreateDeploymentParams{
					ReleaseID:     preview.TargetReleaseID,
					EnvironmentID: preview.EnvironmentID, Status: status,
					Note: sql.NullString{String: fmt.Sprintf(
						"Rollback of #%d: %s → %s (from successful deployment #%d)",
						sourceID,
						preview.SourceVersion,
						preview.TargetVersion,
						preview.TargetDeploymentID,
					), Valid: true},
				}, preview.TargetDeploymentID)
			if err != nil {
				return err
			}
			return q.CreateDeploymentRollback(ctx,
				db.CreateDeploymentRollbackParams{
					DeploymentID:       result.Deployment.ID,
					SourceDeploymentID: sourceID,
					TargetDeploymentID: preview.TargetDeploymentID,
					SourceVersion:      preview.SourceVersion,
					TargetVersion:      preview.TargetVersion,
				})
		})
	})
	return result, err
}

type RollbackGateError struct{ Reason string }

func (e *RollbackGateError) Error() string { return e.Reason }
