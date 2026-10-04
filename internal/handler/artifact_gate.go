package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/views/pages"
	"github.com/go-chi/chi/v5"
)

type ArtifactGateHandler struct {
	repo   *repository.Repository
	runner *runner.DeploymentRunner
}

func NewArtifactGateHandler(
	repo *repository.Repository,
	r *runner.DeploymentRunner,
) *ArtifactGateHandler {
	return &ArtifactGateHandler{repo: repo, runner: r}
}

func gateHTTPError(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	message string,
) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).
			Encode(map[string]string{"error": message}); err != nil {
			return
		}
		return
	}
	http.Error(w, message, status)
}

func (h *ArtifactGateHandler) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		gateHTTPError(w, r, 400, "Invalid deployment ID")
		return
	}
	if err := h.repo.MaintainArtifactGates(r.Context()); err != nil {
		gateHTTPError(w, r, 500, "Gate maintenance failed")
		return
	}
	gates, err := h.repo.Queries.ListArtifactGates(r.Context(), id)
	if err != nil {
		gateHTTPError(w, r, 500, "Gate lookup failed")
		return
	}
	info := make([]pages.ArtifactGateInfo, 0, len(gates))
	for _, gate := range gates {
		info = append(info, pages.NewArtifactGateInfo(gate))
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(info); err != nil {
			return
		}
		return
	}
	if err := pages.ArtifactGates(info).Render(r.Context(), w); err != nil {
		gateHTTPError(w, r, 500, "Gate rendering failed")
	}
}

func (h *ArtifactGateHandler) Approve(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, true)
}

func (h *ArtifactGateHandler) Reject(w http.ResponseWriter, r *http.Request) {
	h.decide(w, r, false)
}

func (h *ArtifactGateHandler) decide(
	w http.ResponseWriter,
	r *http.Request,
	approve bool,
) {
	u := auth.UserFromContext(r.Context())
	if u == nil || u.Role != "admin" {
		gateHTTPError(w, r, 403, "Admin access required")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		gateHTTPError(w, r, 400, "Invalid deployment ID")
		return
	}
	index, err := strconv.ParseInt(chi.URLParam(r, "stepIndex"), 10, 64)
	if err != nil || index < 0 {
		gateHTTPError(w, r, 400, "Invalid step index")
		return
	}
	var request struct {
		Revision int64  `json:"revision"`
		SHA256   string `json:"sha256"`
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		if !ReadJSON(w, r, &request) {
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			gateHTTPError(w, r, 400, "Invalid gate decision")
			return
		}
		request.Revision, err = strconv.ParseInt(
			r.FormValue("revision"),
			10,
			64,
		)
		if err != nil {
			gateHTTPError(w, r, 400, "Invalid revision")
			return
		}
		request.SHA256 = r.FormValue("sha256")
	}
	if approve {
		err = h.repo.ApproveArtifact(
			r.Context(),
			id,
			index,
			request.Revision,
			u.ID,
			request.SHA256,
		)
	} else {
		err = h.repo.RejectArtifactGate(
			r.Context(),
			id,
			index,
			request.Revision,
			request.SHA256,
		)
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, repository.ErrArtifactGate) {
			status = http.StatusConflict
		}
		gateHTTPError(
			w,
			r,
			status,
			"Artifact decision failed; refresh and review the current gate",
		)
		return
	}
	if approve {
		deployment, err := h.repo.Queries.GetDeployment(r.Context(), id)
		if err != nil {
			gateHTTPError(w, r, 500, "Continuation lookup failed")
			return
		}
		go h.runner.Run(
			context.Background(),
			deployment.ID,
			deployment.ReleaseID,
			deployment.EnvironmentID,
		)
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).
			Encode(map[string]bool{"accepted": true}); err != nil {
			return
		}
		return
	}
	http.Redirect(
		w,
		r,
		"/deployments/"+strconv.FormatInt(id, 10),
		http.StatusSeeOther,
	)
}
