package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"durpdeploy/views/pages"
	"github.com/go-chi/chi/v5"
)

func (h *DeploymentHandler) GetVerification(
	w http.ResponseWriter, r *http.Request,
) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}
	check, err := h.repo.Queries.GetDeploymentVerification(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		check.Status = "disabled"
	} else if err != nil {
		http.Error(
			w,
			"Cannot load verification",
			http.StatusInternalServerError,
		)
		return
	}
	deployment, err := h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}
	if err := pages.DeploymentVerificationStatus(deployment, check).
		Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"Cannot render verification",
			http.StatusInternalServerError,
		)
	}
}
