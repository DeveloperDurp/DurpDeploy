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
	source, err := h.repo.Queries.GetDeploymentStepSource(
		r.Context(),
		gate.DeploymentID,
	)
	var steps []struct {
		ReviewPath   string `json:"approval_review_path"`
		ReviewFormat string `json:"approval_review_format"`
	}
	if err != nil || json.Unmarshal([]byte(source.StepsJson), &steps) != nil ||
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
		if err := json.NewEncoder(w).Encode(struct {
			Resources      []artifact.TerraformResourceChange `json:"resources"`
			ReviewSource   string                             `json:"review_source"`
			ReviewVerified bool                               `json:"review_verified"`
		}{resources, "step_output", false}); err != nil {
			return
		}
		return
	}
	if err := pages.TerraformPlanView(resources).
		Render(r.Context(), w); err != nil {
		gateHTTPError(w, r, 500, "Review rendering failed")
	}
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
