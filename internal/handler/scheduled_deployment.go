package handler

import (
	"database/sql"
	"errors"
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

const invalidScheduleOffsetMessage = "Invalid offset"

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
	_, err := scheduleOffset(r)
	if err != nil {
		http.Error(w, invalidScheduleOffsetMessage, http.StatusBadRequest)
		return
	}
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

	page, err := h.loadSchedulePage(r, projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if isFragmentRequest(r) {
		if err := pages.ScheduledDeploymentsList(project, page).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := pages.ScheduledDeploymentsListPage(
		project,
		page,
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
	offset, err := scheduleOffset(r)
	if err != nil {
		http.Error(w, invalidScheduleOffsetMessage, http.StatusBadRequest)
		return
	}
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

	envs, err := h.availableEnvsForDeployPage(r, project, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if isFragmentRequest(r) {
		if err := pages.ScheduledDeploymentForm(
			project, releases, envs, nil, "", offset,
		).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := pages.ScheduledDeploymentFormPage(
		project, releases, envs, nil, "", offset, r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Create handles POST /projects/{id}/schedules.
func (h *ScheduledDeploymentHandler) Create(
	w http.ResponseWriter,
	r *http.Request,
) {
	offset, err := scheduleOffset(r)
	if err != nil {
		http.Error(w, invalidScheduleOffsetMessage, http.StatusBadRequest)
		return
	}
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

	_, noteSubmitted := r.Form["note"]
	submitted := &db.ScheduledDeployment{
		ProjectID:     projectID,
		ReleaseID:     releaseID,
		EnvironmentID: environmentID,
		Cron:          strings.TrimSpace(r.FormValue("cron")),
		Enabled:       0,
		Note: sql.NullString{
			String: r.FormValue("note"),
			Valid:  noteSubmitted,
		},
	}
	if isTruthy(r.FormValue("enabled")) {
		submitted.Enabled = 1
	}

	if submitted.Cron == "" {
		h.renderFormError(w, r, projectID, submitted,
			"Cron expression is required.")
		return
	}
	if strings.HasPrefix(submitted.Cron, "TZ=") ||
		strings.HasPrefix(submitted.Cron, "CRON_TZ=") {
		h.renderFormError(
			w,
			r,
			projectID,
			submitted,
			"TZ= and CRON_TZ= prefixes are not supported. Use server local time only.",
		)
		return
	}

	sched, err := h.parser.Parse(submitted.Cron)
	if err != nil {
		h.renderFormError(
			w,
			r,
			projectID,
			submitted,
			"Invalid cron expression. Use 5-field standard cron (minute hour day month weekday). Descriptors like @hourly and TZ= prefixes are not supported.",
		)
		return
	}

	release, err := h.repo.Queries.GetDeploymentRelease(
		r.Context(),
		submitted.ReleaseID,
	)
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
	if err := h.repo.ValidateExecutableRelease(
		r.Context(),
		release.ID,
	); err != nil {
		if writeArtifactError(w, err) {
			return
		}
		if errors.Is(err, repository.ErrLegacyServerStep) {
			http.Error(
				w,
				repository.ErrLegacyServerStep.Error(),
				http.StatusConflict,
			)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	nextRun := sched.Next(time.Now())
	if nextRun.IsZero() {
		h.renderFormError(w, r, projectID, submitted,
			"Cron expression is unsatisfiable (never fires).")
		return
	}

	note := sql.NullString{
		String: submitted.Note.String,
		Valid:  submitted.Note.String != "",
	}

	_, err = h.repo.CreateScheduledDeployment(
		r.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     projectID,
			ReleaseID:     submitted.ReleaseID,
			EnvironmentID: submitted.EnvironmentID,
			Cron:          submitted.Cron,
			NextRunAt:     nextRun.Unix(),
			Enabled:       submitted.Enabled,
			LastFiredAt:   sql.NullInt64{},
			Note:          note,
		},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Release not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(
		w,
		r,
		pages.ScheduleListURL(projectID, offset),
		http.StatusSeeOther,
	)
}

// EditForm renders the edit form pre-filled.
func (h *ScheduledDeploymentHandler) EditForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	offset, err := scheduleOffset(r)
	if err != nil {
		http.Error(w, invalidScheduleOffsetMessage, http.StatusBadRequest)
		return
	}
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

	releases, err := h.repo.Queries.ListReleasesByProject(
		r.Context(),
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	envs, err := h.availableEnvsForDeployPage(r, project, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if isFragmentRequest(r) {
		if err := pages.ScheduledDeploymentForm(
			project, releases, envs, &schedule, "", offset,
		).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := pages.ScheduledDeploymentFormPage(
		project, releases, envs, &schedule, "", offset, r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// Update handles PUT /projects/{id}/schedules/{schedId}.
func (h *ScheduledDeploymentHandler) Update(
	w http.ResponseWriter,
	r *http.Request,
) {
	offset, err := scheduleOffset(r)
	if err != nil {
		http.Error(w, invalidScheduleOffsetMessage, http.StatusBadRequest)
		return
	}
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

	_, noteSubmitted := r.Form["note"]
	submitted := &db.ScheduledDeployment{
		ID:            schedID,
		ProjectID:     projectID,
		ReleaseID:     releaseID,
		EnvironmentID: environmentID,
		Cron:          strings.TrimSpace(r.FormValue("cron")),
		Enabled:       0,
		Note: sql.NullString{
			String: r.FormValue("note"),
			Valid:  noteSubmitted,
		},
	}
	if isTruthy(r.FormValue("enabled")) {
		submitted.Enabled = 1
	}

	if submitted.Cron == "" {
		h.renderFormError(w, r, projectID, submitted,
			"Cron expression is required.")
		return
	}
	if strings.HasPrefix(submitted.Cron, "TZ=") ||
		strings.HasPrefix(submitted.Cron, "CRON_TZ=") {
		h.renderFormError(
			w,
			r,
			projectID,
			submitted,
			"TZ= and CRON_TZ= prefixes are not supported. Use server local time only.",
		)
		return
	}

	sched, err := h.parser.Parse(submitted.Cron)
	if err != nil {
		h.renderFormError(
			w,
			r,
			projectID,
			submitted,
			"Invalid cron expression. Use 5-field standard cron (minute hour day month weekday). Descriptors like @hourly and TZ= prefixes are not supported.",
		)
		return
	}

	release, err := h.repo.Queries.GetDeploymentRelease(
		r.Context(),
		submitted.ReleaseID,
	)
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
	if err := h.repo.ValidateExecutableRelease(
		r.Context(),
		release.ID,
	); err != nil {
		if writeArtifactError(w, err) {
			return
		}
		if errors.Is(err, repository.ErrLegacyServerStep) {
			http.Error(
				w,
				repository.ErrLegacyServerStep.Error(),
				http.StatusConflict,
			)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	nextRun := sched.Next(time.Now())
	if nextRun.IsZero() {
		h.renderFormError(w, r, projectID, submitted,
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
		String: submitted.Note.String,
		Valid:  submitted.Note.String != "",
	}

	_, err = h.repo.UpdateScheduledDeployment(
		r.Context(),
		db.UpdateScheduledDeploymentParams{
			ProjectID:     projectID,
			ReleaseID:     submitted.ReleaseID,
			EnvironmentID: submitted.EnvironmentID,
			Cron:          submitted.Cron,
			NextRunAt:     nextRun.Unix(),
			Enabled:       submitted.Enabled,
			LastFiredAt:   existing.LastFiredAt,
			Note:          note,
			ID:            schedID,
		},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Release or schedule not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(
		w,
		r,
		pages.ScheduleListURL(projectID, offset),
		http.StatusSeeOther,
	)
}

// Delete handles DELETE /projects/{id}/schedules/{schedId}.
func (h *ScheduledDeploymentHandler) Delete(
	w http.ResponseWriter,
	r *http.Request,
) {
	offset, err := scheduleOffset(r)
	if err != nil {
		http.Error(w, invalidScheduleOffsetMessage, http.StatusBadRequest)
		return
	}
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

	if err := h.repo.Queries.DeleteScheduledDeployment(
		r.Context(),
		schedID,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(
		w,
		r,
		pages.ScheduleListURL(projectID, offset),
		http.StatusSeeOther,
	)
}

// Toggle flips the enabled flag. If enabling, re-computes next_run_at.
func (h *ScheduledDeploymentHandler) Toggle(
	w http.ResponseWriter,
	r *http.Request,
) {
	offset, err := scheduleOffset(r)
	if err != nil {
		http.Error(w, invalidScheduleOffsetMessage, http.StatusBadRequest)
		return
	}
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
	if existing.Enabled == 0 {
		if err := h.repo.ValidateExecutableRelease(
			r.Context(),
			existing.ReleaseID,
		); err != nil {
			if writeArtifactError(w, err) {
				return
			}
			if errors.Is(err, repository.ErrLegacyServerStep) {
				http.Error(
					w,
					repository.ErrLegacyServerStep.Error(),
					http.StatusConflict,
				)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
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
		h.List(w, r)
		return
	}

	http.Redirect(
		w,
		r,
		pages.ScheduleListURL(projectID, offset),
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
	offset, _ := scheduleOffset(r)
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
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
	envs, err := h.availableEnvsForDeployPage(r, project, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	WriteFormError(
		w,
		r,
		pages.ScheduledDeploymentForm(
			project,
			releases,
			envs,
			schedule,
			errorMsg,
			offset,
		),
		pages.ScheduledDeploymentFormPage(
			project, releases, envs, schedule, errorMsg, offset, r.URL.Path,
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
