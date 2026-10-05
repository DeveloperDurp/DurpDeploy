package handler

import (
	"archive/tar"
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
		deployment, err := h.repo.Queries.GetDeployment(
			r.Context(),
			gate.DeploymentID,
		)
		if err != nil {
			gateHTTPError(w, r, 500, "Review variables unavailable")
			return
		}
		variables, err := h.repo.ListReleaseVariablesByRelease(
			r.Context(),
			deployment.ReleaseID,
		)
		if err != nil {
			gateHTTPError(w, r, 500, "Review variables unavailable")
			return
		}
		resolved, err := runner.ResolveReleaseVariables(
			variables,
			deployment.EnvironmentID,
		)
		if err != nil {
			gateHTTPError(w, r, 500, "Review variables unavailable")
			return
		}
		var secrets []string
		for _, variable := range resolved {
			if variable.Secret {
				secrets = append(secrets, variable.Value)
			}
		}
		reader := tar.NewReader(file)
		for {
			header, err := reader.Next()
			if err != nil {
				gateHTTPError(w, r, 409, "Review unavailable")
				return
			}
			if header.Name != step.ReviewPath {
				continue
			}
			if header.Typeflag != tar.TypeReg || header.Size > 1<<20 {
				gateHTTPError(w, r, 409, "Review unavailable")
				return
			}
			raw, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
			if err == nil {
				resources, err = artifact.TerraformReview(raw, secrets)
			}
			if err != nil {
				gateHTTPError(w, r, 409, "Review unavailable")
				return
			}
			break
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
