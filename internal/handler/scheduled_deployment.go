package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type ScheduledDeploymentHandler struct {
	repo   *repository.Repository
	parser cron.Parser
}

func NewScheduledDeploymentHandler(
	repo *repository.Repository,
	parser cron.Parser,
) *ScheduledDeploymentHandler {
	return &ScheduledDeploymentHandler{repo: repo, parser: parser}
}

// List renders the scheduled deployments for a project.
func (h *ScheduledDeploymentHandler) List(
	w http.ResponseWriter,
	r *http.Request,
) {
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

	schedules, err := h.repo.Queries.ListScheduledDeploymentsByProject(
		r.Context(),
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	releases, err := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items, err := h.buildScheduleListItems(r.Context(), projectID, schedules, releases)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.ScheduledDeploymentsList(project, items).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := pages.ScheduledDeploymentsListPage(
		project,
		items,
		r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// NewForm renders the create form.
func (h *ScheduledDeploymentHandler) NewForm(
	w http.ResponseWriter,
	r *http.Request,
) {
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

	envs, err := h.availableEnvsForDeployPage(r, project, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.ScheduledDeploymentForm(
			project, releases, envs, nil, "",
		).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := pages.ScheduledDeploymentFormPage(
		project, releases, envs, nil, "", r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Create handles POST /projects/{id}/schedules.
func (h *ScheduledDeploymentHandler) Create(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	releaseID, err := strconv.ParseInt(r.FormValue("release_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
		return
	}

	environmentID, err := strconv.ParseInt(
		r.FormValue("environment_id"),
		10,
		64,
	)
	if err != nil {
		http.Error(w, "Invalid environment ID", http.StatusBadRequest)
		return
	}

	cronExpr := strings.TrimSpace(r.FormValue("cron"))
	if cronExpr == "" {
		h.renderFormError(w, r, projectID, nil,
			"Cron expression is required.")
		return
	}
	if strings.HasPrefix(cronExpr, "TZ=") || strings.HasPrefix(cronExpr, "CRON_TZ=") {
		h.renderFormError(w, r, projectID, nil,
			"TZ= and CRON_TZ= prefixes are not supported. Use server local time only.")
		return
	}

	sched, err := h.parser.Parse(cronExpr)
	if err != nil {
		h.renderFormError(w, r, projectID, nil,
			"Invalid cron expression. Use 5-field standard cron (minute hour day month weekday). Descriptors like @hourly and TZ= prefixes are not supported.")
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), releaseID)
	if err != nil {
		http.Error(w, "Release not found", http.StatusBadRequest)
		return
	}
	if release.ProjectID != projectID {
		http.Error(
			w,
			"Release does not belong to this project",
			http.StatusBadRequest,
		)
		return
	}

	nextRun := sched.Next(time.Now())
	if nextRun.IsZero() {
		h.renderFormError(w, r, projectID, nil,
			"Cron expression is unsatisfiable (never fires).")
		return
	}

	note := sql.NullString{
		String: r.FormValue("note"),
		Valid:  r.FormValue("note") != "",
	}
	enabled := int64(1)
	if !isTruthy(r.FormValue("enabled")) {
		enabled = 0
	}

	_, err = h.repo.Queries.CreateScheduledDeployment(
		r.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     projectID,
			ReleaseID:     releaseID,
			EnvironmentID: environmentID,
			Cron:          cronExpr,
			NextRunAt:     nextRun.Unix(),
			Enabled:       enabled,
			LastFiredAt:   sql.NullInt64{},
			Note:          note,
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/schedules", projectID),
		http.StatusSeeOther,
	)
}

// EditForm renders the edit form pre-filled.
func (h *ScheduledDeploymentHandler) EditForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	schedID, err := strconv.ParseInt(chi.URLParam(r, "schedId"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
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

	schedule, err := h.repo.Queries.GetScheduledDeployment(r.Context(), schedID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Schedule not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if schedule.ProjectID != projectID {
		http.Error(
			w,
			"Schedule does not belong to this project",
			http.StatusBadRequest,
		)
		return
	}

	releases, err := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	envs, err := h.availableEnvsForDeployPage(r, project, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.ScheduledDeploymentForm(
			project, releases, envs, &schedule, "",
		).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := pages.ScheduledDeploymentFormPage(
		project, releases, envs, &schedule, "", r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Update handles PUT /projects/{id}/schedules/{schedId}.
func (h *ScheduledDeploymentHandler) Update(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	schedID, err := strconv.ParseInt(chi.URLParam(r, "schedId"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	releaseID, err := strconv.ParseInt(r.FormValue("release_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
		return
	}

	environmentID, err := strconv.ParseInt(
		r.FormValue("environment_id"),
		10,
		64,
	)
	if err != nil {
		http.Error(w, "Invalid environment ID", http.StatusBadRequest)
		return
	}

	cronExpr := strings.TrimSpace(r.FormValue("cron"))
	if cronExpr == "" {
		h.renderFormError(w, r, projectID, &db.ScheduledDeployment{ID: schedID},
			"Cron expression is required.")
		return
	}
	if strings.HasPrefix(cronExpr, "TZ=") || strings.HasPrefix(cronExpr, "CRON_TZ=") {
		h.renderFormError(w, r, projectID, &db.ScheduledDeployment{ID: schedID},
			"TZ= and CRON_TZ= prefixes are not supported. Use server local time only.")
		return
	}

	sched, err := h.parser.Parse(cronExpr)
	if err != nil {
		h.renderFormError(w, r, projectID, &db.ScheduledDeployment{ID: schedID},
			"Invalid cron expression. Use 5-field standard cron (minute hour day month weekday). Descriptors like @hourly and TZ= prefixes are not supported.")
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), releaseID)
	if err != nil {
		http.Error(w, "Release not found", http.StatusBadRequest)
		return
	}
	if release.ProjectID != projectID {
		http.Error(
			w,
			"Release does not belong to this project",
			http.StatusBadRequest,
		)
		return
	}

	nextRun := sched.Next(time.Now())
	if nextRun.IsZero() {
		h.renderFormError(w, r, projectID, &db.ScheduledDeployment{ID: schedID},
			"Cron expression is unsatisfiable (never fires).")
		return
	}

	existing, err := h.repo.Queries.GetScheduledDeployment(r.Context(), schedID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Schedule not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if existing.ProjectID != projectID {
		http.Error(
			w,
			"Schedule does not belong to this project",
			http.StatusBadRequest,
		)
		return
	}

	note := sql.NullString{
		String: r.FormValue("note"),
		Valid:  r.FormValue("note") != "",
	}
	enabled := int64(1)
	if !isTruthy(r.FormValue("enabled")) {
		enabled = 0
	}

	_, err = h.repo.Queries.UpdateScheduledDeployment(
		r.Context(),
		db.UpdateScheduledDeploymentParams{
			ProjectID:     projectID,
			ReleaseID:     releaseID,
			EnvironmentID: environmentID,
			Cron:          cronExpr,
			NextRunAt:     nextRun.Unix(),
			Enabled:       enabled,
			LastFiredAt:   existing.LastFiredAt,
			Note:          note,
			ID:            schedID,
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/schedules", projectID),
		http.StatusSeeOther,
	)
}

// Delete handles DELETE /projects/{id}/schedules/{schedId}.
func (h *ScheduledDeploymentHandler) Delete(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	schedID, err := strconv.ParseInt(chi.URLParam(r, "schedId"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	existing, err := h.repo.Queries.GetScheduledDeployment(r.Context(), schedID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Schedule not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if existing.ProjectID != projectID {
		http.Error(
			w,
			"Schedule does not belong to this project",
			http.StatusBadRequest,
		)
		return
	}

	if err := h.repo.Queries.DeleteScheduledDeployment(r.Context(), schedID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/schedules", projectID),
		http.StatusSeeOther,
	)
}

// Toggle flips the enabled flag. If enabling, re-computes next_run_at.
func (h *ScheduledDeploymentHandler) Toggle(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	schedID, err := strconv.ParseInt(chi.URLParam(r, "schedId"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid schedule ID", http.StatusBadRequest)
		return
	}

	existing, err := h.repo.Queries.GetScheduledDeployment(r.Context(), schedID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Schedule not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if existing.ProjectID != projectID {
		http.Error(
			w,
			"Schedule does not belong to this project",
			http.StatusBadRequest,
		)
		return
	}

	updated, err := h.repo.Queries.ToggleScheduledDeploymentEnabled(
		r.Context(),
		schedID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If now enabled, recompute next_run_at from now.
	if updated.Enabled == 1 {
		sched, err := h.parser.Parse(updated.Cron)
		if err == nil {
			next := sched.Next(time.Now())
			if !next.IsZero() {
				if err := h.repo.Queries.UpdateScheduledDeploymentNextRun(
					r.Context(),
					db.UpdateScheduledDeploymentNextRunParams{
						NextRunAt: next.Unix(),
						ID:        updated.ID,
					},
				); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
	}

	if r.Header.Get("HX-Request") == "true" {
		project, err := h.repo.Queries.GetProject(r.Context(), projectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		schedules, err := h.repo.Queries.ListScheduledDeploymentsByProject(
			r.Context(),
			projectID,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		releases, err := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items, err := h.buildScheduleListItems(r.Context(), projectID, schedules, releases)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := pages.ScheduledDeploymentsList(
			project,
			items,
		).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/schedules", projectID),
		http.StatusSeeOther,
	)
}

// renderFormError is a small helper that re-renders the form with an error.
func (h *ScheduledDeploymentHandler) renderFormError(
	w http.ResponseWriter,
	r *http.Request,
	projectID int64,
	schedule *db.ScheduledDeployment,
	errorMsg string,
) {
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	releases, err := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	envs, err := h.availableEnvsForDeployPage(r, project, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	WriteFormError(
		w,
		r,
		pages.ScheduledDeploymentForm(project, releases, envs, schedule, errorMsg),
		pages.ScheduledDeploymentFormPage(
			project, releases, envs, schedule, errorMsg, r.URL.Path,
		),
	)
}

// availableEnvsForDeployPage reuses the same logic from DeploymentHandler.
func (h *ScheduledDeploymentHandler) availableEnvsForDeployPage(
	r *http.Request,
	project db.Project,
	release *db.Release,
) ([]pages.AvailableEnv, error) {
	dh := NewDeploymentHandler(h.repo, nil)
	return dh.availableEnvsForDeployPage(r, project, release)
}

func (h *ScheduledDeploymentHandler) buildScheduleListItems(
	ctx context.Context,
	projectID int64,
	schedules []db.ScheduledDeployment,
	releases []db.Release,
) ([]pages.ScheduledDeploymentListItem, error) {
	envs, err := h.repo.Queries.ListEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	envName := make(map[int64]string, len(envs))
	for _, e := range envs {
		envName[e.ID] = e.Name
	}
	relVer := make(map[int64]string, len(releases))
	for _, r := range releases {
		relVer[r.ID] = r.Version
	}
	items := make([]pages.ScheduledDeploymentListItem, 0, len(schedules))
	for _, s := range schedules {
		items = append(items, pages.ScheduledDeploymentListItem{
			Schedule:        s,
			ReleaseVersion:  relVer[s.ReleaseID],
			EnvironmentName: envName[s.EnvironmentID],
		})
	}
	return items, nil
}
