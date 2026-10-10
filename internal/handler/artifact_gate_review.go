package handler

import (
	"archive/tar"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/runner"
	"durpdeploy/views/pages"
)

func (h *ArtifactGateHandler) Review(w http.ResponseWriter, r *http.Request) {
	file, gate := h.openGateBundle(w, r)
	if file == nil {
		return
	}
	defer file.Close()
	steps, err := h.artifactReviewSteps(r.Context(), gate.DeploymentID)
	if err != nil ||
		gate.StepIndex < 0 || gate.StepIndex >= int64(len(steps)) {
		gateHTTPError(w, r, 409, "Review configuration unavailable")
		return
	}
	step := steps[gate.StepIndex]
	resources := []artifact.TerraformResourceChange{}
	if step.ReviewFormat == "terraform" {
		secrets, err := h.artifactReviewSecrets(r.Context(), gate.DeploymentID)
		if err != nil {
			gateHTTPError(w, r, 500, "Review variables unavailable")
			return
		}
		resources, err = readTerraformGateReview(file, step.ReviewPath, secrets)
		if err != nil {
			gateHTTPError(w, r, 409, "Review unavailable")
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(artifact.TerraformReviewResponse{
			Resources: resources, ReviewSource: "step_output",
		}); err != nil {
			return
		}
		return
	}
	if err := pages.TerraformPlanView(resources).
		Render(r.Context(), w); err != nil {
		gateHTTPError(w, r, 500, "Review rendering failed")
	}
}

type artifactReviewStep struct {
	ReviewPath   string `json:"approval_review_path"`
	ReviewFormat string `json:"approval_review_format"`
}

func (h *ArtifactGateHandler) artifactGateInfo(
	ctx context.Context,
	deploymentID int64,
) ([]pages.ArtifactGateInfo, error) {
	deployment, err := h.repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	gates, err := h.repo.Queries.ListArtifactGates(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	info := make([]pages.ArtifactGateInfo, 0, len(gates))
	if len(gates) == 0 {
		return info, nil
	}
	steps, err := h.artifactReviewSteps(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	now, err := h.repo.Queries.CurrentUnixTime(ctx)
	if err != nil {
		return nil, err
	}
	for _, gate := range gates {
		item := pages.NewArtifactGateInfo(gate)
		item.DecisionReady = deployment.Status == "awaiting_artifact_approval" &&
			gate.Status == "awaiting" &&
			gate.ExpiresAt > now
		item.Publishing = deployment.Status == "publishing_artifact"
		if gate.StepIndex >= 0 && gate.StepIndex < int64(len(steps)) {
			item.ReviewFormat = steps[gate.StepIndex].ReviewFormat
		}
		info = append(info, item)
	}
	return info, nil
}

func (h *ArtifactGateHandler) artifactReviewSteps(
	ctx context.Context,
	deploymentID int64,
) ([]artifactReviewStep, error) {
	source, err := h.repo.Queries.GetDeploymentStepSource(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	var steps []artifactReviewStep
	err = json.Unmarshal([]byte(source.StepsJson), &steps)
	return steps, err
}

func (h *ArtifactGateHandler) artifactReviewSecrets(
	ctx context.Context,
	deploymentID int64,
) ([]string, error) {
	deployment, err := h.repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	variables, err := h.repo.ListReleaseVariablesByRelease(
		ctx,
		deployment.ReleaseID,
	)
	if err != nil {
		return nil, err
	}
	resolved, err := runner.ResolveReleaseVariables(
		variables,
		deployment.EnvironmentID,
	)
	if err != nil {
		return nil, err
	}
	var secrets []string
	for _, variable := range resolved {
		if variable.Secret {
			secrets = append(secrets, variable.Value)
		}
	}
	return secrets, nil
}

func readTerraformGateReview(
	file io.Reader,
	path string,
	secrets []string,
) ([]artifact.TerraformResourceChange, error) {
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err != nil {
			return nil, err
		}
		if header.Name != path {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size > 1<<20 {
			return nil, artifact.ErrGateConfig
		}
		raw, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
		if err != nil {
			return nil, err
		}
		return artifact.TerraformReview(raw, secrets)
	}
}
