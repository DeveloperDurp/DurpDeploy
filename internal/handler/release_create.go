package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

func (h *ReleaseHandler) CreateRelease(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	version := strings.TrimSpace(r.FormValue("version"))

	if version == "" {
		h.renderReleaseCreateError(w, r, pages.ReleaseFormState{
			ProjectID: projectID, Error: "Version is required",
		})
		return
	}

	allowMissing := false
	if value := r.FormValue("allow_missing_package"); value != "" {
		allowMissing, err = strconv.ParseBool(value)
		if err != nil {
			http.Error(
				w,
				"Invalid allow_missing_package",
				http.StatusBadRequest,
			)
			return
		}
	}
	_, err = CreateReleaseSnapshotWithPackage(r.Context(), h.repo,
		ReleaseSnapshotRequest{ProjectID: projectID, Version: version,
			AllowMissingPackage: allowMissing})
	if err != nil {
		var missing *repository.PackageMissingError
		if errors.As(err, &missing) {
			h.renderReleaseCreateError(w, r, pages.ReleaseFormState{
				ProjectID: projectID, Version: version,
				Error: missing.Error(), PackageMissing: true,
			})
			return
		}
		if writeArtifactError(w, err) {
			return
		}
		if IsUniqueViolation(err) {
			h.renderReleaseCreateError(w, r, pages.ReleaseFormState{
				ProjectID: projectID, Version: version,
				Error: "A release with this version already exists",
			})
			return
		}

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderCreatedRelease(w, r, projectID)
}

func (h *ReleaseHandler) renderCreatedRelease(
	w http.ResponseWriter,
	r *http.Request,
	projectID int64,
) {
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(
			w,
			r,
			fmt.Sprintf("/projects/%d/releases", projectID),
			http.StatusSeeOther,
		)
		return
	}
	releases, err := h.repo.Queries.ListReleasesByProject(
		r.Context(), projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views, err := buildReleaseViews(r.Context(), h.repo, releases)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.ReleasesFragment(project, views, "").
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
