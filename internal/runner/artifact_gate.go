package runner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/staging"
)

const approvedMount = "/approved"

func (r *DeploymentRunner) stagingRestoreError(
	ctx context.Context, deploymentID int64, err error,
) error {
	if !errors.Is(err, staging.ErrInvalid) {
		return errors.Join(repository.ErrArtifactGate, err)
	}
	_, logErr := r.repo.Queries.CreateDeploymentLog(
		ctx,
		db.CreateDeploymentLogParams{
			DeploymentID: deploymentID,
			Line:         "Saved approval context does not satisfy the current staging policy",
		},
	)
	return errors.Join(repository.ErrArtifactGate, logErr, fmt.Errorf(
		"approval context does not satisfy the current staging policy: %w", err,
	))
}

func (r *DeploymentRunner) captureArtifactGate(
	ctx context.Context,
	deploymentID, stepIndex int64,
	step deploymentStep,
	stage artifactStage,
) (result error) {
	if err := r.validateArtifactGateExport(ctx, stage.keeper); err != nil {
		return err
	}
	file, err := artifact.OpenGateSpool()
	if err != nil {
		return err
	}
	defer func() {
		result = artifact.SpoolCleanupError(ctx, result, file.Close())
	}()
	command := r.engine.command(
		ctx,
		"exec",
		stage.keeper,
		"tar",
		"-cf",
		"-",
		"-C",
		deploymentStageMount,
		".",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	hash := sha256.New()
	checksum, review, copyErr := artifact.CopyGateBundleWithLimits(
		ctx,
		stdout,
		staging.LimitWriter(
			io.MultiWriter(
				file,
				hash,
			),
			r.repo.ResourceLimits().Staging.ArchiveBytes,
		),
		step.ApprovalArtifactPath,
		step.ApprovalReviewPath,
		step.ApprovalReviewFormat,
		r.repo.ResourceLimits().Artifact,
	)
	closeErr := stdout.Close()
	waitErr := command.Wait()
	if err := errors.Join(copyErr, closeErr, waitErr); err != nil {
		return errors.Join(repository.ErrArtifactGate, err)
	}
	if _, err := staging.ReadWithLimits(ctx, file, r.repo.ResourceLimits().Staging); err != nil {
		return errors.Join(
			repository.ErrArtifactGate,
			fmt.Errorf(
				"approval context does not satisfy the current staging policy: %w",
				err,
			),
		)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(review)
	if err != nil {
		return err
	}
	now, err := r.repo.Queries.CurrentUnixTime(ctx)
	if err != nil {
		return err
	}
	return r.repo.SaveArtifactGate(ctx, db.CreateArtifactGateParams{
		DeploymentID:   deploymentID,
		StepIndex:      stepIndex,
		ArtifactPath:   step.ApprovalArtifactPath,
		ArtifactSha256: checksum,
		BundleSha256: hex.EncodeToString(
			hash.Sum(nil),
		),
		BundleSize: info.Size(),
		Review:     string(encoded),
		CreatedAt:  now,
		ExpiresAt:  now + 24*60*60,
	}, file)
}

func (r *DeploymentRunner) restoreArtifactGate(
	ctx context.Context,
	deploymentID, stepIndex int64,
) (stage artifactStage, result error) {
	gate, err := r.repo.Queries.GetArtifactGate(
		ctx,
		db.GetArtifactGateParams{
			DeploymentID: deploymentID,
			StepIndex:    stepIndex,
		},
	)
	if err != nil {
		return stage, repository.ErrArtifactGate
	}
	now, err := r.repo.Queries.CurrentUnixTime(ctx)
	if err != nil {
		return stage, err
	}
	if gate.Status != "approved" || gate.ExpiresAt <= now ||
		!gate.ApprovedBy.Valid {
		return stage, repository.ErrArtifactGate
	}
	file, err := artifact.OpenGateSpool()
	if err != nil {
		return stage, err
	}
	defer func() {
		result = artifact.SpoolCleanupError(ctx, result, file.Close())
	}()
	if err := r.repo.WriteArtifactGateBundle(
		ctx,
		r.repo.Queries,
		gate,
		file,
	); err != nil {
		return stage, r.stagingRestoreError(ctx, deploymentID, err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return stage, err
	}
	if err := artifact.ValidateGateBundleNodes(ctx, file, r.repo.ResourceLimits().Artifact.StagingNodes); err != nil {
		if errors.Is(err, artifact.ErrInvalid) {
			_, logErr := r.repo.Queries.CreateDeploymentLog(
				ctx,
				db.CreateDeploymentLogParams{
					DeploymentID: deploymentID,
					Line:         "Saved approval context does not satisfy the current staging-node limit",
				},
			)
			err = errors.Join(err, logErr)
		}
		return stage, errors.Join(repository.ErrArtifactGate, err)
	}
	if _, err := staging.ReadWithLimits(ctx, file, r.repo.ResourceLimits().Staging); err != nil {
		return stage, r.stagingRestoreError(ctx, deploymentID, err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return stage, err
	}
	stage, err = r.stageDeployment(ctx, deploymentID, map[string]string{})
	if err != nil {
		return stage, err
	}
	command := r.engine.command(
		ctx,
		"exec",
		"--interactive",
		stage.keeper,
		"tar",
		"-xf",
		"-",
		"-C",
		deploymentStageMount,
	)
	command.Stdin = file
	if err := command.Run(); err != nil {
		return stage, repository.ErrArtifactGate
	}
	return stage, nil
}

func (r *DeploymentRunner) pinArtifactGateImages(
	ctx context.Context,
	deploymentID int64,
	steps []deploymentStep,
) error {
	for index, step := range steps {
		if step.ExecutionTarget == "agent" {
			continue
		}
		id, err := r.repo.Queries.GetArtifactGateImage(
			ctx,
			db.GetArtifactGateImageParams{
				DeploymentID: deploymentID,
				StepIndex:    int64(index),
			},
		)
		if err == nil {
			if err := r.retainArtifactGateImage(ctx, deploymentID,
				int64(index), id); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		id, err = r.artifactGateImageID(ctx, step.ContainerImage)
		if err != nil {
			return err
		}
		if err := r.saveArtifactGateImage(
			ctx,
			db.CreateArtifactGateImageParams{
				DeploymentID: deploymentID,
				StepIndex:    int64(index),
				ImageID:      id,
			},
		); err != nil {
			return err
		}
	}
	return nil
}
