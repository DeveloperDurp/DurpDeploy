package handler

import (
	"fmt"
	"net/http"
	"strings"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/provenance"
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
	source := provenance.Source{
		RepositoryURL: r.FormValue("repository_url"),
		CommitSHA:     r.FormValue("commit_sha"),
		ArtifactRef:   r.FormValue("artifact_ref"),
		CIRunURL:      r.FormValue("ci_run_url"),
	}
	if err := source.ValidateWithScrubber(h.repo.NewScrubber(nil)); err != nil {
		// Discard rejected metadata so credentials cannot enter rendered errors.
		h.renderReleaseCreateError(w, r, pages.ReleaseFormState{
			ProjectID: projectID, Version: version, Error: err.Error(),
		})
		return
	}

	if version == "" {
		h.renderReleaseCreateError(w, r, pages.ReleaseFormState{
			ProjectID: projectID, Source: source, Error: "Version is required",
		})
		return
	}

	release, err := CreateReleaseWithProvenance(r.Context(), h.repo,
		projectID, version, source, repository.IdempotencyRequest{})
	if err != nil {
		if writeArtifactError(w, err) {
			return
		}
		if IsUniqueViolation(err) {
			h.renderReleaseCreateError(w, r, pages.ReleaseFormState{
				ProjectID: projectID, Version: version, Source: source,
				Error: "A release with this version already exists. " +
					"Enter a different version to create a new release.",
			})
			return
		}

		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	audit.SetReleaseProvenance(r, release)
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
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views, err := loadReleaseViews(r.Context(), h.repo, projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.ReleasesFragment(project, views, "").
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
