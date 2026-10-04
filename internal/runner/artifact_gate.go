package runner

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const approvedMount = "/approved"

func (r *DeploymentRunner) captureArtifactGate(
	ctx context.Context,
	deploymentID, stepIndex int64,
	step deploymentStep,
	stage artifactStage,
) (result error) {
	file, err := artifact.OpenGateSpool()
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
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
	limited := &gateBoundedWriter{
		target:    io.MultiWriter(file, hash),
		remaining: artifact.MaxDownload,
	}
	checksum, review, copyErr := artifact.CopyGateBundle(
		ctx,
		stdout,
		limited,
		step.ApprovalArtifactPath,
		step.ApprovalReviewPath,
		step.ApprovalReviewFormat,
	)
	closeErr := stdout.Close()
	waitErr := command.Wait()
	if err := errors.Join(copyErr, closeErr, waitErr); err != nil {
		return repository.ErrArtifactGate
	}
	if _, err := file.Seek(0, 0); err != nil {
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
		BundleSize: artifact.MaxDownload - limited.remaining,
		Review:     string(encoded),
		CreatedAt:  now,
		ExpiresAt:  now + 24*60*60,
	}, file)
}

type gateBoundedWriter struct {
	target    io.Writer
	remaining int64
}

func (w *gateBoundedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, artifact.ErrInvalid
	}
	n, err := w.target.Write(data)
	w.remaining -= int64(n)
	return n, err
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
	defer func() { result = errors.Join(result, file.Close()) }()
	if err := r.repo.WriteArtifactGateBundle(
		ctx,
		r.repo.Queries,
		gate,
		file,
	); err != nil {
		return stage, err
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
		output, err := r.engine.command(ctx, "image", "inspect", "--format={{.Id}}", step.ContainerImage).
			Output()
		if err != nil {
			if _, err := r.engine.command(ctx, "pull", "--quiet", step.ContainerImage).
				Output(); err != nil {
				return repository.ErrArtifactGate
			}
			output, err = r.engine.command(ctx, "image", "inspect", "--format={{.Id}}", step.ContainerImage).
				Output()
			if err != nil {
				return repository.ErrArtifactGate
			}
		}
		id = strings.TrimSpace(string(output))
		if id == "" {
			return repository.ErrArtifactGate
		}
		if err := r.retainArtifactGateImage(ctx, deploymentID,
			int64(index), id); err != nil {
			return err
		}
		if err := r.repo.Queries.CreateArtifactGateImage(
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
