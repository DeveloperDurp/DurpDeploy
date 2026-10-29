package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/config"
)

const artifactMount = "/artifacts"

// Unix-socket runtimes share the server kernel's page size. Each staging node
// can waste one page, including implicit directories. Startup checks overflow.
func artifactStagingCapacity(limits config.ArtifactLimits) int64 {
	return limits.ExtractedBytes + limits.StagingNodes*int64(os.Getpagesize())
}

const artifactHelperImage = "harbor.durp.info/dockerhub/library/alpine@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507"

type artifactStage struct {
	volume, keeper      string
	creationUnconfirmed bool
}

func (r *DeploymentRunner) stageArtifact(
	ctx context.Context,
	deploymentID int64,
) (result artifactStage, err error) {
	_, err = r.repo.Queries.GetDeploymentArtifact(ctx, deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	steps, err := r.repo.Queries.ListDeploymentSteps(ctx, deploymentID)
	if err != nil {
		return result, fmt.Errorf("load frozen package steps: %w", err)
	}
	local := false
	for _, step := range steps {
		if step.ExecutionTarget != "agent" {
			local = true
			break
		}
	}
	if local && r.localErr != nil {
		return result, r.localErr
	}
	if local {
		recorded, err := r.recordContainerNamespace(ctx, deploymentID)
		if err != nil {
			return result, err
		}
		if recorded != 1 {
			return result, errContainerCleanup
		}
	}
	download, err := r.repo.DownloadDeploymentArtifact(ctx, deploymentID)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.Remove(download.Path)) }()
	if !local {
		return result, nil
	}
	directory, err := artifact.ExtractZIPWithLimits(ctx, download.Path,
		r.repo.ResourceLimits().Artifact)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	archive, err := os.CreateTemp(
		filepath.Dir(download.Path),
		"durpdeploy-artifact-*.tar",
	)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, archive.Close(), os.Remove(archive.Name())) }()
	if err := artifact.WriteTar(ctx, directory, archive); err != nil {
		return result, err
	}
	if _, err := archive.Seek(0, 0); err != nil {
		return result, err
	}
	result, err = r.createStagingVolume(ctx, deploymentID, artifactMount)
	if err != nil {
		return result, err
	}
	command := r.engine.command(
		ctx,
		"exec",
		"--interactive",
		result.keeper,
		"tar",
		"-xf",
		"-",
		"-C",
		artifactMount,
	)
	command.Stdin = archive
	if _, err := command.CombinedOutput(); err != nil {
		return result, fmt.Errorf("stage artifact files: %w", err)
	}
	return result, nil
}

func (r *DeploymentRunner) artifactMountArgs(stage artifactStage) []string {
	if stage.volume == "" {
		return nil
	}
	options := ":ro,nocopy"
	if r.engine.kind == "podman" {
		options = ":ro,noexec,nosuid,nodev,nocopy"
	}
	return []string{"--volume=" + stage.volume + ":" + artifactMount + options}
}

func (r *DeploymentRunner) cleanupArtifact(deploymentID int64) error {
	transferErr := r.replaceTransfer(deploymentID, nil)
	r.mu.Lock()
	stages, exists := r.staging[deploymentID]
	r.mu.Unlock()
	if !exists {
		return transferErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cleanupErr := transferErr
	for _, stage := range stages {
		if stage.creationUnconfirmed {
			cleanupErr = errors.Join(cleanupErr, errContainerCleanup)
		}
		output, err := r.engine.command(ctx, r.engine.removeArgs(stage.keeper)...).
			CombinedOutput()
		if err = r.engine.removalError(output, err); err != nil {
			r.recordCleanupFailure(ctx, deploymentID, fmt.Errorf(
				"remove staging keeper %q: %w: %s",
				stage.keeper, err, strings.TrimSpace(string(output)),
			))
			cleanupErr = errors.Join(cleanupErr, errContainerCleanup, err)
			continue
		}
		output, err = r.engine.command(ctx, "volume", "rm", stage.volume).
			CombinedOutput()
		if err != nil &&
			!strings.Contains(
				strings.ToLower(string(output)),
				"no such volume",
			) {
			r.recordCleanupFailure(ctx, deploymentID, fmt.Errorf(
				"remove staging volume %q: %w: %s",
				stage.volume, err, strings.TrimSpace(string(output)),
			))
			cleanupErr = errors.Join(cleanupErr, errContainerCleanup, err)
		}
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	r.mu.Lock()
	delete(r.staging, deploymentID)
	r.mu.Unlock()
	return nil
}

func (r *DeploymentRunner) recordCleanupFailure(
	ctx context.Context, deploymentID int64, cleanupErr error,
) {
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		5*time.Second,
	)
	defer cancel()
	variables, err := r.repo.ListDeploymentVariables(ctx, deploymentID)
	var secrets []string
	message := "Staging cleanup unconfirmed: runtime detail omitted because secret redaction policy is unavailable"
	if err == nil {
		for _, variable := range variables {
			if variable.Secret != 0 {
				secrets = append(secrets, variable.Value.String)
			}
		}
		message = "Staging cleanup unconfirmed: " + cleanupErr.Error()
	}
	writer := &broadcastWriter{
		broker: r.broker, repo: r.repo, deploymentID: deploymentID,
		ctx: ctx, scrubber: r.repo.NewScrubber(secrets),
	}
	if err := writer.writeLine(writer.scrubber.Scrub(message), ""); err != nil {
		slog.Error(
			"persist staging cleanup diagnostic",
			"deployment_id",
			deploymentID,
			"err",
			err,
		)
	}
}

func (r *DeploymentRunner) reconcileArtifactVolumes(ctx context.Context) error {
	output, err := r.engine.command(ctx, "volume", "ls", "--quiet", "--filter=label=io.durpdeploy.artifact=true", "--filter=label=io.durpdeploy.namespace="+r.engine.scope()).
		Output()
	if err != nil {
		return fmt.Errorf("list artifact volumes: %w", err)
	}
	for _, volume := range strings.Fields(string(output)) {
		if _, err := r.engine.command(ctx, "volume", "rm", volume).
			CombinedOutput(); err != nil {
			return fmt.Errorf("remove artifact volume: %w", err)
		}
	}
	return nil
}
