package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

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

func NewDeploymentHandler(repo *repository.Repository, runner *runner.DeploymentRunner) *DeploymentHandler {
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

func (h *DeploymentHandler) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	releaseID, err := strconv.ParseInt(r.FormValue("release_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
		return
	}

	environmentID, err := strconv.ParseInt(r.FormValue("environment_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid environment ID", http.StatusBadRequest)
		return
	}

	force := isTruthy(r.FormValue("force"))

	release, err := h.repo.Queries.GetRelease(r.Context(), releaseID)
	if err != nil {
		http.Error(w, "Release not found", http.StatusBadRequest)
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), release.ProjectID)
	if err != nil {
		http.Error(w, "Project not found", http.StatusBadRequest)
		return
	}

	violation, blocked := h.checkPromotionGate(r, project, release, environmentID)
	if blocked {
		// Hard restriction: force cannot bypass.
		if !violation.bypassable {
			h.renderGateError(w, r, project, release, environmentID, violation.reason)
			return
		}
		// Bypassable: force is required to proceed.
		if !force {
			h.renderGateError(w, r, project, release, environmentID, violation.reason)
			return
		}
	}

	forcedFlag := int64(0)
	if force && violation != nil && violation.bypassable {
		forcedFlag = 1
	}

	deployment, err := h.repo.Queries.CreateDeployment(r.Context(), db.CreateDeploymentParams{
		ReleaseID:     releaseID,
		EnvironmentID: environmentID,
		Status:        "pending",
		StartedAt:     sql.NullInt64{},
		FinishedAt:    sql.NullInt64{},
		Forced:        forcedFlag,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	go h.runner.Run(context.Background(), deployment.ID, releaseID, environmentID)

	http.Redirect(w, r, fmt.Sprintf("/deployments/%d", deployment.ID), http.StatusSeeOther)
}

// checkPromotionGate enforces two rules when a project has a lifecycle:
//  1. The target environment must be a stage in the lifecycle (force cannot bypass).
//  2. There must be a successful deployment of the same release to the previous stage.
//
// Returns the violation (for the message) and true if blocked. Returns nil, false if
// the deploy is allowed. Free-floating projects (no lifecycle) always return nil, false.
func (h *DeploymentHandler) checkPromotionGate(r *http.Request, project db.Project, release db.Release, environmentID int64) (*gateViolation, bool) {
	state, err := evaluateGate(r.Context(), h.repo, project, release, environmentID)
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

// renderGateError renders a 422 page that re-displays the releases table with
// the gate violation message, so the user can see the error and re-attempt with force.
func (h *DeploymentHandler) renderGateError(w http.ResponseWriter, r *http.Request, project db.Project, release db.Release, environmentID int64, reason string) {
	releases, err := h.repo.Queries.ListReleasesByProject(r.Context(), project.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views, err := buildReleaseViews(r.Context(), h.repo, project, releases)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	if r.Header.Get("HX-Request") == "true" {
		if err := pages.ReleasesFragment(project, views, reason).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := pages.ReleasesPage(project, views, reason, r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// availableEnvironmentsForProject returns the envs a project may deploy to:
// lifecycle stages if it has a lifecycle, otherwise all envs.
func (h *DeploymentHandler) availableEnvironmentsForProject(r *http.Request, project db.Project) ([]db.Environment, error) {
	all, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		return nil, err
	}
	if !project.LifecycleID.Valid {
		return all, nil
	}
	stageIDs, err := h.repo.Queries.ListLifecycleStageEnvironmentIDs(r.Context(), project.LifecycleID.Int64)
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

func isTruthy(s string) bool {
	switch s {
	case "true", "1", "on", "yes":
		return true
	}
	return false
}

func (h *DeploymentHandler) GetDeployment(w http.ResponseWriter, r *http.Request) {
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

	environment, err := h.repo.Queries.GetEnvironment(r.Context(), deployment.EnvironmentID)
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
		if err := pages.DeploymentDetail(project, release, environment, deployment, logs).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.DeploymentDetailPage(project, release, environment, deployment, logs, r.URL.Path).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func (h *DeploymentHandler) GetDeploymentStatus(w http.ResponseWriter, r *http.Request) {
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

	if err := pages.StatusBadgeContainer(deployment).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *DeploymentHandler) CancelDeployment(w http.ResponseWriter, r *http.Request) {
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

	if deployment.Status != "running" {
		http.Error(w, "Deployment is not running", http.StatusBadRequest)
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
		if err := pages.StatusBadgeContainer(deployment).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		http.Redirect(w, r, fmt.Sprintf("/deployments/%d", deployment.ID), http.StatusSeeOther)
	}
}

func (h *DeploymentHandler) ListDeployments(w http.ResponseWriter, r *http.Request) {
	deployments, err := h.repo.Queries.ListDeployments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]pages.DeploymentListItem, len(deployments))
	for i, d := range deployments {
		release, err := h.repo.Queries.GetRelease(r.Context(), d.ReleaseID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		project, err := h.repo.Queries.GetProject(r.Context(), release.ProjectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		env, err := h.repo.Queries.GetEnvironment(r.Context(), d.EnvironmentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items[i] = pages.DeploymentListItem{
			Deployment:      d,
			ProjectName:     project.Name,
			ReleaseVersion:  release.Version,
			EnvironmentName: env.Name,
		}
	}

	if r.Header.Get("HX-Request") == "true" {
		if err := pages.DeploymentsList(items).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.DeploymentsListPage(items, r.URL.Path).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
