package handler

import (
	"context"
	"database/sql"
	"encoding/json"
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

	releases, err := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
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
		if err := pages.ReleasesFragment(project, views, "").Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.ReleasesPage(project, views, "", r.URL.Path).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// buildReleaseViews turns a list of releases into the per-row data the releases
// table needs (release + per-env gate state). One query per release to evaluate
// the gate, which is fine for the small N this app handles.
func buildReleaseViews(ctx context.Context, repo *repository.Repository, project db.Project, releases []db.Release) ([]pages.ReleaseView, error) {
	views := make([]pages.ReleaseView, len(releases))
	for i, rel := range releases {
		envs, err := availableEnvsForRelease(ctx, repo, project, rel)
		if err != nil {
			return nil, err
		}
		pageEnvs := make([]pages.AvailableEnv, len(envs))
		for j, e := range envs {
			pageEnvs[j] = pages.AvailableEnv{
				Environment: e.Environment,
				State:       pages.GateState{Deployable: e.State.deployable, Reason: e.State.reason, Bypassable: e.State.bypassable},
			}
		}
		views[i] = pages.ReleaseView{Release: rel, Envs: pageEnvs}
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
		releases, _ := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
		views, _ := buildReleaseViews(r.Context(), h.repo, project, releases)
		WriteFormError(w, r, pages.ReleaseForm(projectID, "Version is required"), pages.ReleasesPage(project, views, "Version is required", r.URL.Path))
		return
	}

	// Query current steps for project
	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Serialize steps to JSON
	type stepSnapshot struct {
		Name       string `json:"name"`
		ScriptBody string `json:"script_body"`
		SortOrder  int64  `json:"sort_order"`
	}

	snapshots := make([]stepSnapshot, len(steps))
	for i, step := range steps {
		snapshots[i] = stepSnapshot{
			Name:       step.Name,
			ScriptBody: step.ScriptBody,
			SortOrder:  step.SortOrder,
		}
	}

	stepsJSON, err := json.Marshal(snapshots)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Start transaction
	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	qtx := h.repo.Queries.WithTx(tx)

	// Insert release
	releaseParams := db.CreateReleaseParams{
		ProjectID: projectID,
		Version:   version,
		StepsJson: string(stepsJSON),
	}

	release, err := qtx.CreateRelease(r.Context(), releaseParams)
	if err != nil {
		if IsUniqueViolation(err) {
			project, _ := h.repo.Queries.GetProject(r.Context(), projectID)
			releases, _ := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
			views, _ := buildReleaseViews(r.Context(), h.repo, project, releases)
			WriteFormError(w, r, pages.ReleaseForm(projectID, "A release with this version already exists"), pages.ReleasesPage(project, views, "A release with this version already exists", r.URL.Path))
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Query current variables for project and snapshot them
	variables, err := h.repo.Queries.ListVariablesByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, variable := range variables {
		varParams := db.CreateReleaseVariableParams{
			ReleaseID:     release.ID,
			Name:          variable.Name,
			Value:         variable.Value,
			EnvironmentID: variable.EnvironmentID,
		}
		if _, err := qtx.CreateReleaseVariable(r.Context(), varParams); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		releases, err := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
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

		if err := pages.ReleasesFragment(project, views, "").Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		http.Redirect(w, r, fmt.Sprintf("/projects/%d/releases", projectID), http.StatusSeeOther)
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

	variables, err := h.repo.Queries.ListReleaseVariablesByRelease(r.Context(), releaseID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.ReleaseDetailPage(project, release, variables, environments, r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *ReleaseHandler) RefreshRelease(w http.ResponseWriter, r *http.Request) {
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

	// Verify release exists and belongs to project
	release, err := h.repo.Queries.GetRelease(r.Context(), releaseID)
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

	// Fetch current steps and serialize
	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type stepSnapshot struct {
		Name       string `json:"name"`
		ScriptBody string `json:"script_body"`
		SortOrder  int64  `json:"sort_order"`
	}
	snapshots := make([]stepSnapshot, len(steps))
	for i, step := range steps {
		snapshots[i] = stepSnapshot{
			Name:       step.Name,
			ScriptBody: step.ScriptBody,
			SortOrder:  step.SortOrder,
		}
	}
	stepsJSON, err := json.Marshal(snapshots)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Start transaction
	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	qtx := h.repo.Queries.WithTx(tx)

	// Update steps_json
	if _, err := qtx.UpdateRelease(r.Context(), db.UpdateReleaseParams{
		ID:        releaseID,
		ProjectID: projectID,
		Version:   release.Version,
		StepsJson: string(stepsJSON),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Delete existing release variables
	if err := qtx.DeleteReleaseVariablesByRelease(r.Context(), releaseID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-insert current variables
	variables, err := h.repo.Queries.ListVariablesByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, v := range variables {
		if _, err := qtx.CreateReleaseVariable(r.Context(), db.CreateReleaseVariableParams{
			ReleaseID:     releaseID,
			Name:          v.Name,
			Value:         v.Value,
			EnvironmentID: v.EnvironmentID,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/projects/%d/releases/%d", projectID, releaseID), http.StatusSeeOther)
}
