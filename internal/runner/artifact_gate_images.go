package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"durpdeploy/internal/repository"
)

const gateImageLabel = "io.durpdeploy.gate-image"

func (r *DeploymentRunner) retainArtifactGateImage(
	ctx context.Context, deploymentID, stepIndex int64, imageID string,
) error {
	name := fmt.Sprintf("%s-%s-gate-image-%d-%d",
		r.engine.kind, r.engine.namespace, deploymentID, stepIndex)
	output, err := r.engine.command(ctx, "inspect",
		"--format={{.Image}}", name).Output()
	if err == nil {
		if strings.TrimSpace(string(output)) != imageID {
			return repository.ErrArtifactGate
		}
		return nil
	}
	// Never started: no script, deployment environment, or deployment mounts.
	args := []string{"create", "--pull=never",
		"--network=none", "--entrypoint=/bin/true", "--name=" + name,
		"--label=io.durpdeploy.namespace=" + r.engine.scope() + ":gate-images",
		"--label=" + gateImageLabel + "=" + strconv.FormatInt(deploymentID, 10)}
	if r.engine.kind == "podman" {
		args = append(args, "--image-volume=ignore")
	}
	_, err = r.engine.command(ctx, append(args, imageID)...).Output()
	return err
}

// CleanupArtifactGateImages releases runtime references after terminal decisions.
// Database errors keep the references; startup and minute maintenance retry.
func (r *DeploymentRunner) CleanupArtifactGateImages(
	ctx context.Context,
) error {
	output, err := r.engine.command(
		ctx,
		"ps",
		"--all",
		"--filter=label=io.durpdeploy.namespace="+r.engine.scope()+":gate-images",
		"--filter=label="+gateImageLabel,
		"--format={{.Names}}",
	).Output()
	if err != nil {
		return err
	}
	prefix := r.engine.kind + "-" + r.engine.namespace + "-gate-image-"
	for _, name := range strings.Fields(string(output)) {
		if !strings.HasPrefix(name, prefix) {
			return repository.ErrArtifactGate
		}
		id, _, found := strings.Cut(strings.TrimPrefix(name, prefix), "-")
		deploymentID, err := strconv.ParseInt(id, 10, 64)
		if err != nil || !found {
			return repository.ErrArtifactGate
		}
		deployment, err := r.repo.Queries.GetDeployment(ctx, deploymentID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			switch deployment.Status {
			case "pending", "queued", "pending_approval", "running",
				"publishing_artifact", "awaiting_artifact_approval":
				continue
			}
		}
		args := r.engine.removeArgs(name)
		args = append(args[:len(args)-1], "--volumes", name)
		if err := r.engine.command(ctx, args...).
			Run(); err != nil {
			return err
		}
	}
	return nil
}
