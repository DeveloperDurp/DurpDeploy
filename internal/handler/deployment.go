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

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/views/pages"
)

type DeploymentHandler struct {
	repo   *repository.Repository
	runner *runner.DeploymentRunner
}

func NewDeploymentHandler(
	repo *repository.Repository,
	runner *runner.DeploymentRunner,
) *DeploymentHandler {
	return &DeploymentHandler{repo: repo, runner: runner}
}

// gateViolation describes a single reason a deployment is blocked by a project's
// lifecycle. The message is shown verbatim in the 422 response and as the body
// of the confirm() dialog when force=true is being used.
type gateViolation struct {
	project    db.Project
	reason     string
	bypassable bool // true = force=true can override; false = hard restriction
}

// NewDeploymentPage renders the dedicated /projects/{id}/deploy page.
// Loads the project, its releases (newest first), and the per-env gate
// state for the env dropdown. 404s on missing project.
//
// Optional query: ?release_id=N pre-selects the release and computes
// release-aware gate state (an env is deployable only when its prior
// stage has succeeded with the same release). Without release_id, every
// lifecycle stage is shown as deployable.
func (h *DeploymentHandler) NewDeploymentPage(
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

	releases, err := h.repo.Queries.ListReleasesByProject(
		r.Context(),
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var selectedRelease *db.Release
	if ridStr := r.URL.Query().Get("release_id"); ridStr != "" {
		if rid, err := strconv.ParseInt(ridStr, 10, 64); err == nil {
			for i := range releases {
				if releases[i].ID == rid {
					selectedRelease = &releases[i]
					break
				}
			}
		}
	}

	envs, err := h.availableEnvsForDeployPage(r, project, selectedRelease)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.DeployFormPage(project, releases, selectedRelease, envs, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// availableEnvsForDeployPage computes the per-env gate state for the
// deploy page's env dropdown. The behaviour splits three ways:
//   - Free-floating project (no lifecycle): every env is deployable.
//   - Lifecycle-bound, no release picked: every lifecycle stage is
//     shown as deployable. The user has to pick a release first; once
//     they do, the URL reloads with ?release_id= and the per-release
//     gate is applied.
//   - Lifecycle-bound, release picked: only stages whose prior stage
//     has succeeded with this release (or which is the first stage)
//     are deployable. Other stages are bypassable via force. Stages
//     already at this version are hidden — there's nothing to do.
//
// On a lifecycle-bound project the envs are returned in lifecycle stage
// order (dev before test before prod) so the dropdown reads as a deploy
// path. Non-stage envs are always hidden.
func (h *DeploymentHandler) availableEnvsForDeployPage(
	r *http.Request,
	project db.Project,
	release *db.Release,
) ([]pages.AvailableEnv, error) {
	if !project.LifecycleID.Valid {
		all, err := h.repo.Queries.ListEnvironments(r.Context())
		if err != nil {
			return nil, err
		}
		out := make([]pages.AvailableEnv, len(all))
		for i, e := range all {
			out[i] = pages.AvailableEnv{
				Environment: e,
				State:       pages.GateState{Deployable: true},
			}
		}
		return out, nil
	}

	stageIDs, err := h.repo.Queries.ListLifecycleStageEnvironmentIDs(
		r.Context(),
		project.LifecycleID.Int64,
	)
	if err != nil {
		return nil, err
	}

	all, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		return nil, err
	}
	envByID := make(map[int64]db.Environment, len(all))
	for _, e := range all {
		envByID[e.ID] = e
	}

	stageEnvs := make([]db.Environment, 0, len(stageIDs))
	for _, id := range stageIDs {
		if e, ok := envByID[id]; ok {
			stageEnvs = append(stageEnvs, e)
		}
	}

	// No release selected: every stage is deployable.
	if release == nil {
		out := make([]pages.AvailableEnv, len(stageEnvs))
		for i, e := range stageEnvs {
			out[i] = pages.AvailableEnv{
				Environment: e,
				State:       pages.GateState{Deployable: true},
			}
		}
		return out, nil
	}

	// Release selected: compute per-stage gate state. Stage 0 is always
	// deployable. Stage n is deployable when stage n-1 has a successful
	// deployment of this release. Stages that already have this release
	// are still included (with AlreadyDeployed=true) so the user can
	// re-run; the view renders them in a separate "Already deployed"
	// optgroup with a warning.
	out := make([]pages.AvailableEnv, 0, len(stageEnvs))
	for i, e := range stageEnvs {
		state := h.stageGateStateForRelease(
			r,
			project,
			release,
			i,
			stageEnvs,
			e,
		)
		out = append(out, pages.AvailableEnv{Environment: e, State: state})
	}
	return out, nil
}

// stageGateStateForRelease computes the gate state for one lifecycle
// stage given a chosen release. Returns AlreadyDeployed=true if this
// stage already has a successful deployment of the release (the env
// shouldn't appear in the dropdown at all in that case).
func (h *DeploymentHandler) stageGateStateForRelease(
	r *http.Request,
	project db.Project,
	release *db.Release,
	index int,
	stageEnvs []db.Environment,
	e db.Environment,
) pages.GateState {
	// Has this stage already seen this release? If yes, hide from the
	// dropdown (nothing to do). Checked for every stage including the
	// first — you wouldn't redeploy to a stage that's already at this
	// version.
	deployed, err := h.repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(
		r.Context(),
		db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
			ReleaseID:     release.ID,
			EnvironmentID: e.ID,
		},
	)
	if err == nil && deployed.ReleaseID == release.ID {
		return pages.GateState{AlreadyDeployed: true}
	}

	// First stage with no prior: always deployable (assuming not already
	// deployed, which we just checked).
	if index == 0 {
		return pages.GateState{Deployable: true}
	}

	// Has the prior stage seen this release?
	prior := stageEnvs[index-1]
	priorDep, err := h.repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(
		r.Context(),
		db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
			ReleaseID:     release.ID,
			EnvironmentID: prior.ID,
		},
	)
	if err == nil && priorDep.ReleaseID == release.ID {
		return pages.GateState{Deployable: true}
	}
	return pages.GateState{
		Deployable: false,
		Bypassable: true,
		Reason: fmt.Sprintf(
			"%s has not been successfully deployed to %s yet. Tick Force to deploy anyway.",
			release.Version,
			prior.Name,
		),
	}
}

// ScheduleDeployment handles POST /projects/{id}/deploy. The body is
// {release_id, environment_id, force}. It validates the release belongs
// to this project, runs the existing promotion gate (with force as the
// override), and creates + dispatches a deployment just like
// CreateDeployment — but reached via a project-scoped URL.
func (h *DeploymentHandler) ScheduleDeployment(
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

	force := isTruthy(r.FormValue("force"))
	note := r.FormValue("note")
	noteParam := sql.NullString{String: note, Valid: note != ""}

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

	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "Project not found", http.StatusBadRequest)
		return
	}

	violation, blocked := h.checkPromotionGate(
		r,
		project,
		release,
		environmentID,
	)
	if blocked {
		// Hard restriction: force cannot bypass.
		if !violation.bypassable {
			h.renderDeployGateError(w, r, project, release, violation.reason)
			return
		}
		// Bypassable: force is required to proceed.
		if !force {
			h.renderDeployGateError(w, r, project, release, violation.reason)
			return
		}
	}

	requiresApproval, err := h.stageRequiresApproval(r, project, environmentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	initialStatus := "pending"
	if requiresApproval {
		initialStatus = "pending_approval"
	}

	forcedFlag := int64(0)
	if force && violation != nil && violation.bypassable {
		forcedFlag = 1
	}

	deployment, err := h.repo.Queries.CreateDeployment(
		r.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     releaseID,
			EnvironmentID: environmentID,
			Status:        initialStatus,
			StartedAt:     sql.NullInt64{},
			FinishedAt:    sql.NullInt64{},
			Forced:        forcedFlag,
			Note:          noteParam,
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if initialStatus == "pending" {
		go h.runner.Run(
			context.Background(),
			deployment.ID,
			releaseID,
			environmentID,
		)
	}

	http.Redirect(
		w,
		r,
		fmt.Sprintf("/deployments/%d", deployment.ID),
		http.StatusSeeOther,
	)
}

// renderDeployGateError renders a 422 with the deploy page re-shown and
// the gate reason. Re-renders the same page so the user can fix their
// selection (or tick Force) without losing the form state.
func (h *DeploymentHandler) renderDeployGateError(
	w http.ResponseWriter,
	r *http.Request,
	project db.Project,
	release db.Release,
	reason string,
) {
	releases, err := h.repo.Queries.ListReleasesByProject(
		r.Context(),
		project.ID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	envs, err := h.availableEnvsForDeployPage(r, project, &release)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := pages.DeployFormPage(project, releases, &release, envs, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// checkPromotionGate enforces two rules when a project has a lifecycle:
//  1. The target environment must be a stage in the lifecycle (force cannot bypass).
//  2. There must be a successful deployment of the same release to the previous stage.
//
// Returns the violation (for the message) and true if blocked. Returns nil, false if
// the deploy is allowed. Free-floating projects (no lifecycle) always return nil, false.
func (h *DeploymentHandler) checkPromotionGate(
	r *http.Request,
	project db.Project,
	release db.Release,
	environmentID int64,
) (*gateViolation, bool) {
	state, err := evaluateGate(
		r.Context(),
		h.repo,
		project,
		release,
		environmentID,
	)
	if err != nil {
		return &gateViolation{
			project:    project,
			reason:     err.Error(),
			bypassable: false,
		}, true
	}
	if state.deployable {
		return nil, false
	}
	return &gateViolation{
		project:    project,
		reason:     state.reason,
		bypassable: state.bypassable,
	}, true
}

// availableEnvironmentsForProject returns the envs a project may deploy to:
// lifecycle stages if it has a lifecycle, otherwise all envs.
func (h *DeploymentHandler) availableEnvironmentsForProject(
	r *http.Request,
	project db.Project,
) ([]db.Environment, error) {
	all, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		return nil, err
	}
	if !project.LifecycleID.Valid {
		return all, nil
	}
	stageIDs, err := h.repo.Queries.ListLifecycleStageEnvironmentIDs(
		r.Context(),
		project.LifecycleID.Int64,
	)
	if err != nil {
		return nil, err
	}
	idSet := make(map[int64]bool, len(stageIDs))
	for _, id := range stageIDs {
		idSet[id] = true
	}
	out := make([]db.Environment, 0, len(stageIDs))
	for _, e := range all {
		if idSet[e.ID] {
			out = append(out, e)
		}
	}
	return out, nil
}

func (h *DeploymentHandler) stageRequiresApproval(
	r *http.Request,
	project db.Project,
	environmentID int64,
) (bool, error) {
	if !project.LifecycleID.Valid {
		return false, nil
	}
	stages, err := h.repo.Queries.ListLifecycleStages(
		r.Context(), project.LifecycleID.Int64,
	)
	if err != nil {
		return false, err
	}
	for _, s := range stages {
		if s.EnvironmentID == environmentID {
			return s.RequiresApproval != 0, nil
		}
	}
	return false, nil
}

func isTruthy(s string) bool {
	switch s {
	case "true", "1", "on", "yes":
		return true
	}
	return false
}

func (h *DeploymentHandler) GetDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), deployment.ReleaseID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), release.ProjectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environment, err := h.repo.Queries.GetEnvironment(
		r.Context(),
		deployment.EnvironmentID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logs, err := h.repo.Queries.ListDeploymentLogsByDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.DeploymentDetail(project, release, environment, deployment, logs).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.DeploymentDetailPage(project, release, environment, deployment, logs, r.URL.Path).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (h *DeploymentHandler) GetDeploymentStatus(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.StatusBadgeContainer(deployment).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *DeploymentHandler) CancelDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if deployment.Status != "running" &&
		deployment.Status != "pending_approval" {
		http.Error(
			w,
			"Deployment cannot be cancelled in its current state",
			http.StatusBadRequest,
		)
		return
	}

	if err := h.runner.Cancel(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	deployment, err = h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.StatusBadgeContainer(deployment).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		http.Redirect(
			w,
			r,
			fmt.Sprintf("/deployments/%d", deployment.ID),
			http.StatusSeeOther,
		)
	}
}

// ApproveDeployment handles POST /deployments/{id}/approve. Marks the
// deployment as approved and dispatches the runner. Only valid when
// the deployment is in pending_approval status.
func (h *DeploymentHandler) ApproveDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}

	deployment, err := h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if deployment.Status != "pending_approval" {
		http.Error(w, "Deployment is not pending approval", http.StatusConflict)
		return
	}

	approvedBy := strings.TrimSpace(r.FormValue("approved_by"))
	if approvedBy == "" {
		approvedBy = "anonymous"
	}

	if _, err := h.repo.Queries.CreateApproval(
		r.Context(),
		db.CreateApprovalParams{
			DeploymentID: id,
			ApprovedBy:   approvedBy,
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Transition to "pending" so the runner picks it up via the normal path.
	if err := h.repo.Queries.UpdateDeploymentStatus(
		r.Context(),
		db.UpdateDeploymentStatusParams{
			ID:     id,
			Status: "pending",
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	go h.runner.Run(
		context.Background(),
		id,
		deployment.ReleaseID,
		deployment.EnvironmentID,
	)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set(
			"HX-Redirect",
			fmt.Sprintf("/deployments/%d", id),
		)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(
		w, r, fmt.Sprintf("/deployments/%d", id),
		http.StatusSeeOther,
	)
}

func (h *DeploymentHandler) RedeployDeployment(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid deployment ID", http.StatusBadRequest)
		return
	}

	source, err := h.repo.Queries.GetDeployment(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Deployment not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if source.Status != "succeeded" && source.Status != "failed" &&
		source.Status != "cancelled" {
		http.Error(
			w,
			"Source deployment is not in a terminal state",
			http.StatusConflict,
		)
		return
	}

	_, err = h.repo.Queries.GetRelease(r.Context(), source.ReleaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Release not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	note := sql.NullString{
		String: fmt.Sprintf("Re-run of #%d", source.ID),
		Valid:  true,
	}
	deployment, err := h.repo.Queries.CreateDeployment(
		r.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     source.ReleaseID,
			EnvironmentID: source.EnvironmentID,
			Status:        "pending",
			StartedAt:     sql.NullInt64{},
			FinishedAt:    sql.NullInt64{},
			Forced:        0,
			Note:          note,
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	go h.runner.Run(
		context.Background(),
		deployment.ID,
		source.ReleaseID,
		source.EnvironmentID,
	)

	if r.Header.Get("HX-Request") == "true" {
		// HX-Redirect (not 303) so the client does a full-page nav; a
		// body swap would clobber the nav bar's Alpine.js state.
		w.Header().
			Set("HX-Redirect", fmt.Sprintf("/deployments/%d", deployment.ID))
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/deployments/%d", deployment.ID),
		http.StatusSeeOther,
	)
}

func (h *DeploymentHandler) ListDeployments(
	w http.ResponseWriter,
	r *http.Request,
) {
	f := parseDeploymentsFilter(r)

	rows, err := h.repo.Queries.ListDeploymentsWithRefsFiltered(
		r.Context(),
		db.ListDeploymentsWithRefsFilteredParams{
			FProjectID: f.ProjectID,
			FEnvID:     f.EnvID,
			FStatus:    f.Status,
			FFromUnix:  f.FromUnix,
			FToUnix:    f.ToUnix,
			PageOffset: f.Offset,
			PageLimit:  f.Limit,
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]pages.DeploymentListItem, len(rows))
	for i, row := range rows {
		items[i] = pages.DeploymentListItem{
			Deployment: db.Deployment{
				ID:            row.ID,
				ReleaseID:     row.ReleaseID,
				EnvironmentID: row.EnvironmentID,
				Status:        row.Status,
				StartedAt:     row.StartedAt,
				FinishedAt:    row.FinishedAt,
				CreatedAt:     row.CreatedAt,
				Forced:        row.Forced,
				Note:          row.Note,
			},
			ProjectName:     row.ProjectName,
			ReleaseVersion:  row.ReleaseVersion,
			EnvironmentName: row.EnvironmentName,
		}
	}

	total, err := h.repo.Queries.CountDeploymentsWithRefsFiltered(
		r.Context(),
		db.CountDeploymentsWithRefsFilteredParams{
			FProjectID: f.ProjectID,
			FEnvID:     f.EnvID,
			FStatus:    f.Status,
			FFromUnix:  f.FromUnix,
			FToUnix:    f.ToUnix,
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		view := pages.DeploymentsView{
			Items:         items,
			Total:         total,
			Limit:         f.Limit,
			Offset:        f.Offset,
			FilterProject: f.ProjectID,
			FilterEnv:     f.EnvID,
			FilterStatus:  f.Status,
			FilterFrom:    f.FromUnix,
			FilterTo:      f.ToUnix,
		}
		if err := pages.DeploymentsList(view, true).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	projects, err := h.repo.Queries.ListProjects(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	envs, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	view := pages.DeploymentsView{
		Items:         items,
		Total:         total,
		Limit:         f.Limit,
		Offset:        f.Offset,
		FilterProject: f.ProjectID,
		FilterEnv:     f.EnvID,
		FilterStatus:  f.Status,
		FilterFrom:    f.FromUnix,
		FilterTo:      f.ToUnix,
		Projects:      projects,
		Envs:          envs,
	}
	if err := pages.DeploymentsListPage(view, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ponytail: matches the StatusBadge switch; adding a new status here means
// adding it to the switch in views/pages/deployments.templ too.
var allowedStatuses = map[string]struct{}{
	"pending":          {},
	"running":          {},
	"succeeded":        {},
	"failed":           {},
	"cancelled":        {},
	"pending_approval": {},
}

const (
	deploymentsDefaultLimit = 10
	deploymentsMaxLimit     = 100
)

type deploymentsFilter struct {
	ProjectID sql.NullInt64
	EnvID     sql.NullInt64
	Status    sql.NullString
	FromUnix  sql.NullInt64
	ToUnix    sql.NullInt64
	Limit     int64
	Offset    int64
}

func parseDeploymentsFilter(r *http.Request) deploymentsFilter {
	q := r.URL.Query()
	f := deploymentsFilter{Limit: deploymentsDefaultLimit, Offset: 0}

	if s := strings.TrimSpace(q.Get("project_id")); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
			f.ProjectID = sql.NullInt64{Int64: v, Valid: true}
		}
	}
	if s := strings.TrimSpace(q.Get("env_id")); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
			f.EnvID = sql.NullInt64{Int64: v, Valid: true}
		}
	}
	if s := strings.TrimSpace(q.Get("status")); s != "" {
		if _, ok := allowedStatuses[s]; ok {
			f.Status = sql.NullString{String: s, Valid: true}
		}
	}
	if s := strings.TrimSpace(q.Get("from")); s != "" {
		if t, ok := parseDateStart(s); ok {
			f.FromUnix = sql.NullInt64{Int64: t, Valid: true}
		}
	}
	if s := strings.TrimSpace(q.Get("to")); s != "" {
		if t, ok := parseDateEnd(s); ok {
			f.ToUnix = sql.NullInt64{Int64: t, Valid: true}
		}
	}
	if s := strings.TrimSpace(q.Get("limit")); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v > 0 {
			if v > deploymentsMaxLimit {
				v = deploymentsMaxLimit
			}
			f.Limit = v
		}
	}
	if s := strings.TrimSpace(q.Get("offset")); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v >= 0 {
			f.Offset = v
		}
	}
	return f
}

func parseDateStart(s string) (int64, bool) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, false
	}
	return t.UTC().Unix(), true
}

func parseDateEnd(s string) (int64, bool) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, false
	}
	return t.UTC().Add(24*time.Hour - time.Second).Unix(), true
}
