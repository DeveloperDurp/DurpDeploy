package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (h *ReleaseHandler) RefreshRelease(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}
	releaseID, err := strconv.ParseInt(chi.URLParam(r, "releaseId"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
		return
	}
	release, err := h.repo.Queries.GetDeploymentRelease(r.Context(), releaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Release not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if release.ProjectID != projectID {
		http.Error(w, "Release not found", http.StatusNotFound)
		return
	}
	if _, err := RefreshReleaseSnapshot(
		r.Context(),
		h.repo,
		release,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/releases/%d", projectID, releaseID),
		http.StatusSeeOther,
	)
}
