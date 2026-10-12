package handler

import (
	"database/sql"
	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
	"net/http"
)

func (h *ProjectHandler) GetProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	variables, err := h.repo.ListVariablesByProject(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	panel, err := h.buildPanelForProject(r, project, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	panel.Inheritance, err = h.repo.InheritedVariables(
		r.Context(),
		project,
		variables,
	)
	if err != nil {
		http.Error(w, "Unable to load inherited variables", 500)
		return
	}

	if isFragmentRequest(r) {
		if err := pages.ProjectDetail(project, panel, variables, environments).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.ProjectDetailPage(project, panel, variables, environments, r.URL.Path).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// buildPanelForProject returns the lifecycle/env panel data for any project.
// filterEmpty=true drops envs with no deployments from free-floating projects —
// used by the projects list page so the at-a-glance strip stays signal-rich
// even when the system has many envs. The project detail page uses
// filterEmpty=false so users can see the full list of lifecycle stages or
// every env, including untouched ones, when picking a deployment target.
func (h *ProjectHandler) buildPanelForProject(
	r *http.Request,
	project db.Project,
	filterEmpty bool,
) (pages.LifecyclePanel, error) {
	envsByID, err := h.envsByID(r)
	if err != nil {
		return pages.LifecyclePanel{}, err
	}
	releasesByID, err := h.releasesByID(r, project.ID)
	if err != nil {
		return pages.LifecyclePanel{}, err
	}

	if !project.LifecycleID.Valid {
		return h.buildFreeFloatingPanel(
			r,
			project,
			envsByID,
			releasesByID,
			filterEmpty,
		)
	}

	lc, err := h.repo.Queries.GetLifecycle(
		r.Context(),
		project.LifecycleID.Int64,
	)
	if err != nil {
		return pages.LifecyclePanel{}, err
	}
	stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), lc.ID)
	if err != nil {
		return pages.LifecyclePanel{}, err
	}
	views := make([]pages.LifecyclePanelStage, len(stages))
	for i, s := range stages {
		views[i] = h.populateStageView(r, pages.LifecyclePanelStage{
			Stage:       s,
			Environment: envsByID[s.EnvironmentID],
		}, releasesByID)
	}
	return pages.LifecyclePanel{Lifecycle: lc, Stages: views}, nil
}

// buildFreeFloatingPanel renders one row per env, ordered by env ID. When
// filterEmpty is true, envs the project has not deployed to are dropped —
// the projects list page uses this so the dot strip stays signal-rich.
func (h *ProjectHandler) buildFreeFloatingPanel(
	r *http.Request,
	project db.Project,
	envsByID map[int64]db.Environment,
	releasesByID map[int64]db.Release,
	filterEmpty bool,
) (pages.LifecyclePanel, error) {
	allEnvs, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		return pages.LifecyclePanel{}, err
	}
	views := make([]pages.LifecyclePanelStage, 0, len(allEnvs))
	for _, e := range allEnvs {
		stage := h.populateStageView(r, pages.LifecyclePanelStage{
			Stage: db.LifecycleStage{
				EnvironmentID: e.ID,
				SortOrder:     e.ID,
			},
			Environment: e,
		}, releasesByID)
		if filterEmpty && stage.LatestAttempt == nil {
			continue
		}
		views = append(views, stage)
	}
	_ = project
	return pages.LifecyclePanel{Stages: views}, nil
}

// populateStageView fills the latest-deploy / latest-attempt / streak fields
// for a single stage view. Shared by lifecycle and free-floating paths.
//
// ponytail: the per-env queries return deployments from any project, so we
// filter by releasesByID to scope the result to this project. Without this
// filter, project A's panel would show project B's deployments when they
// share an environment. Upgrade path: add project_id to deployments and
// pass it into the queries.
func (h *ProjectHandler) populateStageView(
	r *http.Request,
	v pages.LifecyclePanelStage,
	releasesByID map[int64]db.Release,
) pages.LifecyclePanelStage {
	ctx := r.Context()
	if dep, err := h.repo.Queries.GetLatestSuccessfulDeploymentForEnv(
		ctx,
		v.Environment.ID,
	); err == nil &&
		dep.ReleaseID != 0 {
		if rel, ok := releasesByID[dep.ReleaseID]; ok {
			d := dep
			v.LatestDeployment = &d
			v.LatestVersion = rel.Version
		}
	}
	recents, err := h.repo.Queries.ListRecentDeploymentsForEnv(
		ctx,
		db.ListRecentDeploymentsForEnvParams{
			EnvironmentID: v.Environment.ID,
			Limit:         5,
		},
	)
	if err == nil && len(recents) > 0 {
		// Filter to deployments of releases owned by this project. If none
		// match, the env has no project-relevant history.
		var projectRecents []db.Deployment
		for _, d := range recents {
			if _, ok := releasesByID[d.ReleaseID]; ok {
				projectRecents = append(projectRecents, d)
			}
		}
		if len(projectRecents) > 0 {
			latest := projectRecents[0]
			v.LatestAttempt = &latest
			if rel, ok := releasesByID[latest.ReleaseID]; ok {
				v.AttemptVersion = rel.Version
			}
			v.RecentAttempts = projectRecents
		}
	}
	return v
}

func (h *ProjectHandler) envsByID(
	r *http.Request,
) (map[int64]db.Environment, error) {
	envs, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		return nil, err
	}
	m := make(map[int64]db.Environment, len(envs))
	for _, e := range envs {
		m[e.ID] = e
	}
	return m, nil
}

func (h *ProjectHandler) releasesByID(
	r *http.Request,
	projectID int64,
) (map[int64]db.Release, error) {
	rels, err := h.repo.Queries.ListReleasesByProject(r.Context(), projectID)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]db.Release, len(rels))
	for _, rel := range rels {
		m[rel.ID] = rel
	}
	return m, nil
}
