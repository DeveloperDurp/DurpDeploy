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

	data, err := h.loadProjectPanelData(r, []db.Project{project}, environments)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	panel, err := h.buildPanelForProject(r, project, data, false)
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
	data *projectPanelData,
	filterEmpty bool,
) (pages.LifecyclePanel, error) {
	if !project.LifecycleID.Valid {
		return buildFreeFloatingPanel(project.ID, data, filterEmpty), nil
	}

	lc, loaded := data.lifecycles[project.LifecycleID.Int64]
	if !loaded {
		var err error
		lc, err = h.repo.Queries.GetLifecycle(
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
		data.lifecycles[lc.ID], data.stages[lc.ID] = lc, stages
	}
	stages := data.stages[lc.ID]
	views := make([]pages.LifecyclePanelStage, len(stages))
	for i, s := range stages {
		views[i] = populateStageView(
			project.ID,
			pages.LifecyclePanelStage{
				Stage:       s,
				Environment: data.envsByID[s.EnvironmentID],
			},
			data,
		)
	}
	return pages.LifecyclePanel{Lifecycle: lc, Stages: views}, nil
}

// buildFreeFloatingPanel preserves the loaded environment order. When
// filterEmpty is true, envs the project has not deployed to are dropped —
// the projects list page uses this so the dot strip stays signal-rich.
func buildFreeFloatingPanel(
	projectID int64,
	data *projectPanelData,
	filterEmpty bool,
) pages.LifecyclePanel {
	views := make([]pages.LifecyclePanelStage, 0, len(data.environments))
	for _, e := range data.environments {
		stage := populateStageView(
			projectID,
			pages.LifecyclePanelStage{
				Stage: db.LifecycleStage{
					EnvironmentID: e.ID,
					SortOrder:     e.ID,
				},
				Environment: e,
			},
			data,
		)
		if filterEmpty && stage.LatestAttempt == nil {
			continue
		}
		views = append(views, stage)
	}
	return pages.LifecyclePanel{
		Stages: views, HasEnvironments: len(data.environments) > 0,
	}
}

// populateStageView fills the latest-deploy / latest-attempt / streak fields
// for a single stage view. Shared by lifecycle and free-floating paths.
func populateStageView(
	projectID int64,
	v pages.LifecyclePanelStage,
	data *projectPanelData,
) pages.LifecyclePanelStage {
	for _, row := range data.deployments[[2]int64{projectID, v.Environment.ID}] {
		deployment := row.Deployment
		if deployment.Status == "succeeded" && v.LatestDeployment == nil {
			v.LatestDeployment = &deployment
			v.LatestVersion = row.Version
		}
		if len(v.RecentAttempts) < 5 {
			v.RecentAttempts = append(v.RecentAttempts, deployment)
			if v.LatestAttempt == nil {
				v.LatestAttempt = &deployment
				v.AttemptVersion = row.Version
			}
		}
	}
	return v
}

type projectPanelData struct {
	environments []db.Environment
	envsByID     map[int64]db.Environment
	deployments  map[[2]int64][]db.ListProjectPanelDeploymentsRow
	lifecycles   map[int64]db.Lifecycle
	stages       map[int64][]db.LifecycleStage
}

func (h *ProjectHandler) loadProjectPanelData(
	r *http.Request,
	projects []db.Project,
	environments []db.Environment,
) (*projectPanelData, error) {
	data := &projectPanelData{
		environments: environments,
		envsByID:     make(map[int64]db.Environment, len(environments)),
		deployments:  make(map[[2]int64][]db.ListProjectPanelDeploymentsRow),
		lifecycles:   make(map[int64]db.Lifecycle),
		stages:       make(map[int64][]db.LifecycleStage),
	}
	projectIDs := make([]int64, 0, len(projects))
	for _, project := range projects {
		projectIDs = append(projectIDs, project.ID)
	}
	environmentIDs := make([]int64, 0, len(environments))
	for _, env := range environments {
		data.envsByID[env.ID] = env
		environmentIDs = append(environmentIDs, env.ID)
	}
	rows, err := h.repo.ProjectPanelDeployments(
		r.Context(),
		projectIDs,
		environmentIDs,
	)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		key := [2]int64{row.ProjectID, row.Deployment.EnvironmentID}
		data.deployments[key] = append(data.deployments[key], row)
	}
	return data, nil
}
