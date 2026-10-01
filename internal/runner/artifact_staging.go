package runner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return result, err
	}
	name := fmt.Sprintf(
		"durpdeploy-artifact-%d-%s",
		deploymentID,
		hex.EncodeToString(nonce[:]),
	)
	result = artifactStage{volume: name, keeper: name + "-keeper"}
	r.mu.Lock()
	r.staging[deploymentID] = result
	r.mu.Unlock()
	args := []string{
		"volume",
		"create",
		"--label=io.durpdeploy.artifact=true",
		"--label=io.durpdeploy.namespace=" + r.engine.scope(),
	}
	if r.engine.kind == "docker" {
		args = append(
			args,
			"--driver=local",
			"--opt=type=tmpfs",
			"--opt=device=tmpfs",
			fmt.Sprintf(
				"--opt=o=noexec,nosuid,nodev,size=%d,nr_inodes=%d,uid=65534,gid=65534,mode=0755",
				artifactStagingCapacity(),
				artifact.MaxStagingNodes+1,
			),
		)
	}
	args = append(args, result.volume)
	if _, err := r.engine.command(ctx, args...).CombinedOutput(); err != nil {
		return result, fmt.Errorf("create artifact volume: %w", err)
	}
	keeperMount := result.volume + ":/artifacts:rw,nocopy"
	if r.engine.kind == "podman" {
		keeperMount += ",noexec,nosuid,nodev,U"
	}
	args = []string{
		"run",
		"--detach",
		"--pull=missing",
		"--name=" + result.keeper,
		"--label=io.durpdeploy.namespace=" + r.engine.scope(),
		"--network=none",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--user=65534:65534",
		"--pids-limit=32",
		fmt.Sprintf("--memory=%d", artifactStagingCapacity()+(128<<20)),
		"--cpus=1",
		"--volume=" + keeperMount,
		"--entrypoint=/usr/bin/tail",
		artifactHelperImage,
		"-f", "/dev/null",
	}
	if _, err := r.engine.command(ctx, args...).CombinedOutput(); err != nil {
		return result, fmt.Errorf("start artifact staging: %w", err)
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
	stage, exists := r.staging[deploymentID]
	r.mu.Unlock()
	if !exists {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := r.engine.command(ctx, r.engine.removeArgs(stage.keeper)...).
		CombinedOutput()
	if err = r.engine.removalError(output, err); err != nil {
		return errors.Join(errContainerCleanup, err)
	}
	output, err = r.engine.command(ctx, "volume", "rm", stage.volume).
		CombinedOutput()
	if err != nil &&
		!strings.Contains(strings.ToLower(string(output)), "no such volume") {
		return errors.Join(errContainerCleanup, err)
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
