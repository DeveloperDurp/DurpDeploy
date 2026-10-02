package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"durpdeploy/internal/repository"
	"durpdeploy/internal/verification"
	"durpdeploy/views/pages"
	"github.com/go-chi/chi/v5"
)

func RollbackErrorStatus(err error) int {
	var gateErr *repository.RollbackGateError
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case errors.As(err, &gateErr), errors.Is(err, verification.ErrInvalid):
		return http.StatusUnprocessableEntity
	case errors.Is(err, repository.ErrRollbackConflict),
		errors.Is(err, repository.ErrRollbackTargetChanged),
		errors.Is(err, repository.ErrNoRollbackTarget),
		errors.Is(err, repository.ErrLegacyServerStep),
		errors.Is(err, repository.ErrContainerCleanupUnconfirmed):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func (h *DeploymentHandler) RollbackConfirmation(
	w http.ResponseWriter, r *http.Request,
) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}
	preview, err := h.repo.PreviewRollback(r.Context(), id)
	if err != nil {
		w.WriteHeader(RollbackErrorStatus(err))
		if renderErr := pages.RollbackUnavailable(id, err.Error(), r.URL.Path).
			Render(r.Context(), w); renderErr != nil {
			http.Error(
				w,
				"Cannot render rollback",
				http.StatusInternalServerError,
			)
		}
		return
	}
	if err := pages.RollbackConfirmation(preview, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render rollback", http.StatusInternalServerError)
	}
}

func (h *DeploymentHandler) RollbackDeployment(
	w http.ResponseWriter, r *http.Request,
) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid rollback form", http.StatusBadRequest)
		return
	}
	target, err := strconv.ParseInt(r.FormValue("target_deployment_id"), 10, 64)
	if err != nil || target <= 0 {
		http.Error(w, "Target deployment ID is required", http.StatusBadRequest)
		return
	}
	result, err := h.repo.CreateRollback(r.Context(), id, target)
	if err != nil {
		if writeArtifactError(w, err) {
			return
		}
		w.WriteHeader(RollbackErrorStatus(err))
		if renderErr := pages.RollbackUnavailable(id, err.Error(), r.URL.Path).
			Render(r.Context(), w); renderErr != nil {
			http.Error(
				w,
				"Cannot render rollback",
				http.StatusInternalServerError,
			)
		}
		return
	}
	h.startLocalDeployment(result)
	http.Redirect(w, r, fmt.Sprintf("/deployments/%d", result.Deployment.ID),
		http.StatusSeeOther)
}
