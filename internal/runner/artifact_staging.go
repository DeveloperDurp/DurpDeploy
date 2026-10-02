package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

const artifactMount = "/artifacts"

// Unix-socket runtimes share the server kernel's page size. Each file can waste
// at most one page; logical ZIP contents remain capped at MaxExtracted.
func artifactStagingCapacity() int64 {
	return artifact.MaxExtracted + int64(
		artifact.MaxFiles,
	)*int64(
		os.Getpagesize(),
	)
}

const artifactHelperImage = "docker.io/library/alpine@sha256:3c81aa9a3d770b316568f4499e30461a5cd3fbd7180bd89e28e34894c7845832"

type artifactStage struct{ volume, keeper string }

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
	if r.localErr != nil {
		return result, r.localErr
	}
	recorded, err := r.repo.Queries.RecordContainerNamespace(
		ctx,
		db.RecordContainerNamespaceParams{
			DeploymentID: deploymentID,
			Namespace:    sql.NullString{String: r.engine.scope(), Valid: true},
		},
	)
	if err != nil {
		return result, err
	}
	if recorded != 1 {
		return result, errContainerCleanup
	}
	download, err := r.repo.DownloadDeploymentArtifact(ctx, deploymentID)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.Remove(download.Path)) }()
	directory, err := artifact.ExtractZIP(ctx, download.Path)
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
	r.mu.Lock()
	stages, exists := r.staging[deploymentID]
	r.mu.Unlock()
	if !exists {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var cleanupErr error
	for _, stage := range stages {
		output, err := r.engine.command(ctx, r.engine.removeArgs(stage.keeper)...).
			CombinedOutput()
		if err = r.engine.removalError(output, err); err != nil {
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
