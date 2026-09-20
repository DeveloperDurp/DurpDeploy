package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type ReleaseHandler struct {
	repo *repository.Repository
}

func NewReleaseHandler(repo *repository.Repository) *ReleaseHandler {
	return &ReleaseHandler{repo: repo}
}

func (h *ReleaseHandler) ListReleases(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	releases, err := h.repo.Queries.ListReleasesByProject(
		r.Context(),
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build one entry per (release, env) pair with the current gate state, so
	// the template can decide which envs to show and what tooltip to render.
	views, err := buildReleaseViews(r.Context(), h.repo, project, releases)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.ReleasesFragment(project, views, "").
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.ReleasesPage(project, views, "", r.URL.Path).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// buildReleaseViews turns a list of releases into the per-row data the releases table needs.
func buildReleaseViews(
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	releases []db.Release,
) ([]pages.ReleaseView, error) {
	views := make([]pages.ReleaseView, len(releases))
	for i, rel := range releases {
		envs, err := availableEnvsForRelease(ctx, repo, project, rel)
		if err != nil {
			return nil, err
		}
		mapped := make([]pages.AvailableEnv, len(envs))
		for j, e := range envs {
			mapped[j] = pages.AvailableEnv{
				Environment: e.Environment,
				State: pages.GateState{
					Deployable:      e.State.deployable,
					Reason:          e.State.reason,
					Bypassable:      e.State.bypassable,
					AlreadyDeployed: false,
				},
			}
		}
		views[i] = pages.ReleaseView{Release: rel, Envs: mapped}
	}
	return views, nil
}

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
		project, _ := h.repo.Queries.GetProject(r.Context(), projectID)
		releases, _ := h.repo.Queries.ListReleasesByProject(
			r.Context(),
			projectID,
		)
		views, _ := buildReleaseViews(r.Context(), h.repo, project, releases)
		WriteFormError(
			w,
			r,
			pages.ReleaseForm(projectID, "Version is required"),
			pages.ReleasesPage(
				project,
				views,
				"Version is required",
				r.URL.Path,
			),
		)
		return
	}

	_, err = CreateReleaseSnapshot(r.Context(), h.repo, projectID, version)
	if err != nil {
		if IsUniqueViolation(err) {
			project, _ := h.repo.Queries.GetProject(r.Context(), projectID)
			releases, _ := h.repo.Queries.ListReleasesByProject(
				r.Context(),
				projectID,
			)
			views, _ := buildReleaseViews(
				r.Context(),
				h.repo,
				project,
				releases,
			)
			WriteFormError(
				w,
				r,
				pages.ReleaseForm(
					projectID,
					"A release with this version already exists",
				),
				pages.ReleasesPage(
					project,
					views,
					"A release with this version already exists",
					r.URL.Path,
				),
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		releases, err := h.repo.Queries.ListReleasesByProject(
			r.Context(),
			projectID,
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

		views, err := buildReleaseViews(r.Context(), h.repo, project, releases)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := pages.ReleasesFragment(project, views, "").
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		http.Redirect(
			w,
			r,
			fmt.Sprintf("/projects/%d/releases", projectID),
			http.StatusSeeOther,
		)
	}
}

func (h *ReleaseHandler) GetRelease(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	releaseIDStr := chi.URLParam(r, "releaseId")
	releaseID, err := strconv.ParseInt(releaseIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), releaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Release not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Verify release belongs to project
	if release.ProjectID != projectID {
		http.Error(w, "Release not found", http.StatusNotFound)
		return
	}

	variables, err := h.repo.ListReleaseVariablesByRelease(
		r.Context(),
		releaseID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.ReleaseDetailPage(project, release, variables, environments, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
