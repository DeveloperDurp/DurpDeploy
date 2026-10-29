package runner

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
)

const deploymentStageMount = "/stage"

func (r *DeploymentRunner) recordContainerNamespace(
	ctx context.Context,
	deploymentID int64,
) (int64, error) {
	return r.repo.Queries.RecordContainerNamespace(ctx,
		db.RecordContainerNamespaceParams{
			DeploymentID: deploymentID,
			Namespace:    sql.NullString{String: r.engine.scope(), Valid: true},
		})
}

func (r *DeploymentRunner) stageDeployment(
	ctx context.Context,
	deploymentID int64,
	environment map[string]string,
) (artifactStage, error) {
	if _, exists := environment[containerenv.StageVariable]; exists {
		return artifactStage{}, fmt.Errorf(
			"%s conflicts with a snapshotted variable",
			containerenv.StageVariable,
		)
	}
	if r.localErr != nil {
		return artifactStage{}, r.localErr
	}
	recorded, err := r.recordContainerNamespace(ctx, deploymentID)
	if err != nil {
		return artifactStage{}, fmt.Errorf("record staging namespace: %w", err)
	}
	if recorded != 1 {
		return artifactStage{}, errContainerCleanup
	}
	return r.createStagingVolume(ctx, deploymentID, deploymentStageMount)
}

// ponytail: one writable directory per deployment; immutable publication and
// durable approval retention belong to the intermediate approval workflow.
func (r *DeploymentRunner) createStagingVolume(
	ctx context.Context,
	deploymentID int64,
	mount string,
) (artifactStage, error) {
	if err := ctx.Err(); err != nil {
		return artifactStage{}, err
	}
	// An engine can finish creating resources after its client is killed.
	// Let setup settle before cleanup, even when the deployment is cancelled.
	setupTimeout := 30 * time.Second
	if deadline, exists := ctx.Deadline(); exists {
		setupTimeout = min(setupTimeout, time.Until(deadline))
	}
	setupCtx, cancelSetup := context.WithTimeout(
		context.WithoutCancel(ctx), setupTimeout,
	)
	defer cancelSetup()
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return artifactStage{}, fmt.Errorf("name staging volume: %w", err)
	}
	name := fmt.Sprintf("durpdeploy-artifact-%d-%s", deploymentID,
		hex.EncodeToString(nonce[:]))
	stage := artifactStage{volume: name, keeper: name + "-keeper"}
	artifactLimits := r.repo.ResourceLimits().Artifact
	capacity, nodes := artifactStagingCapacity(
		artifactLimits,
	), artifactLimits.StagingNodes
	if mount == deploymentStageMount {
		limits := r.repo.ResourceLimits().Staging
		capacity = limits.PayloadBytes + limits.Entries*int64(os.Getpagesize())
		nodes = limits.Entries
	}
	r.mu.Lock()
	stageIndex := len(r.staging[deploymentID])
	r.staging[deploymentID] = append(r.staging[deploymentID], stage)
	r.mu.Unlock()
	defer func() {
		if setupCtx.Err() != nil {
			r.mu.Lock()
			r.staging[deploymentID][stageIndex].creationUnconfirmed = true
			r.mu.Unlock()
		}
	}()
	args := []string{
		"volume", "create", "--label=io.durpdeploy.artifact=true",
		"--label=io.durpdeploy.namespace=" + r.engine.scope(),
	}
	if r.engine.kind == "docker" || mount == deploymentStageMount {
		// tmpfs charges security attributes to its inode quota too.
		args = append(args, "--driver=local", "--opt=type=tmpfs",
			"--opt=device=tmpfs", fmt.Sprintf(
				"--opt=o=noexec,nosuid,nodev,size=%d,nr_inodes=%d,uid=65534,gid=65534,mode=0755",
				capacity,
				2*(nodes+1),
			))
	}
	args = append(args, stage.volume)
	if _, err := r.engine.command(setupCtx, args...).CombinedOutput(); err != nil {
		return stage, fmt.Errorf("create staging volume: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return stage, err
	}
	keeperMount := stage.volume + ":" + mount + ":rw,nocopy"
	if r.engine.kind == "podman" {
		keeperMount += ",noexec,nosuid,nodev"
		if mount == deploymentStageMount {
			keeperMount += ",z"
		} else {
			keeperMount += ",U"
		}
	}
	args = []string{
		"run", "--detach", "--pull=missing", "--name=" + stage.keeper,
		"--label=io.durpdeploy.namespace=" + r.engine.scope(),
		"--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--user=65534:65534",
		fmt.Sprintf(
			"--pids-limit=%d",
			r.repo.ResourceLimits().Container.HelperPIDs,
		),
		fmt.Sprintf("--memory=%d", capacity+
			r.repo.ResourceLimits().Container.HelperMemoryOverheadBytes),
		"--cpus=" + strconv.FormatFloat(
			r.repo.ResourceLimits().Container.HelperCPUs, 'f', -1, 64),
		"--volume=" + keeperMount,
		"--entrypoint=/usr/bin/tail", artifactHelperImage,
		"-f", "/dev/null",
	}
	if _, err := r.engine.command(setupCtx, args...).CombinedOutput(); err != nil {
		return stage, fmt.Errorf("start staging keeper: %w", err)
	}
	return stage, ctx.Err()
}

func (r *DeploymentRunner) deploymentMountArgs(stage artifactStage) []string {
	if stage.volume == "" {
		return nil
	}
	options := ":rw,nocopy"
	if r.engine.kind == "podman" {
		options += ",noexec,nosuid,nodev,z"
	}
	return []string{
		"--volume=" + stage.volume + ":" + deploymentStageMount + options,
	}
}
