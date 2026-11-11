package repository

import (
	"context"
	"fmt"
	"slices"

	"durpdeploy/internal/db"
)

// ReleaseReference exposes release identity without its frozen script data.
type ReleaseReference struct {
	ID      int64  `json:"id"`
	Version string `json:"version"`
}

type EnvironmentReleaseAvailability struct {
	EnvironmentID    int64             `json:"environment_id"`
	EnvironmentName  string            `json:"environment_name"`
	CurrentRelease   *ReleaseReference `json:"current_release"`
	UpdateAvailable  bool              `json:"update_available"`
	ReadyToDeploy    bool              `json:"ready_to_deploy"`
	Reason           string            `json:"reason"`
	RequiresApproval bool              `json:"requires_approval"`
}

type ProjectReleaseAvailability struct {
	LatestRelease    *ReleaseReference                `json:"latest_release"`
	UpdatesAvailable bool                             `json:"updates_available"`
	Environments     []EnvironmentReleaseAvailability `json:"environments"`
}

type releaseAvailabilityData struct {
	latest    map[int64]ReleaseReference
	succeeded map[[2]int64]bool
	current   map[[2]int64]*ReleaseReference
	envNames  map[int64]string
}

// ProjectReleaseAvailability compares the newest created release with each
// environment's last successful real deployment. Callers authorize projects.
func (r *Repository) ReleaseAvailability(
	ctx context.Context,
	projects []db.Project,
) (map[int64]ProjectReleaseAvailability, error) {
	result := make(map[int64]ProjectReleaseAvailability, len(projects))
	if len(projects) == 0 {
		return result, nil
	}
	envs, err := r.Queries.ListEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	projectIDs := make([]int64, 0, len(projects))
	envIDs := make([]int64, 0, len(envs))
	for _, p := range projects {
		projectIDs = append(projectIDs, p.ID)
	}
	for _, e := range envs {
		envIDs = append(envIDs, e.ID)
	}
	rows, err := r.ProjectPanelDeployments(ctx, projectIDs, envIDs)
	if err != nil {
		return nil, err
	}
	stages := make(map[int64][]db.LifecycleStage)
	for _, p := range projects {
		if _, loaded := stages[p.LifecycleID.Int64]; !p.LifecycleID.Valid ||
			loaded {
			continue
		}
		stages[p.LifecycleID.Int64], err = r.Queries.ListLifecycleStages(
			ctx,
			p.LifecycleID.Int64,
		)
		if err != nil {
			return nil, err
		}
	}
	return r.ReleaseAvailabilityForPanel(ctx, projects, envs, rows, stages)
}

// ReleaseAvailabilityForPanel reuses request-owned panel data. Reads remain
// bounded by project batches rather than project/environment pairs.
func (r *Repository) ReleaseAvailabilityForPanel(
	ctx context.Context,
	projects []db.Project,
	envs []db.Environment,
	rows []db.ListProjectPanelDeploymentsRow,
	stagesByLifecycle map[int64][]db.LifecycleStage,
) (map[int64]ProjectReleaseAvailability, error) {
	result := make(map[int64]ProjectReleaseAvailability, len(projects))
	projectIDs := make([]int64, 0, len(projects))
	envNames := make(map[int64]string, len(envs))
	for _, p := range projects {
		projectIDs = append(projectIDs, p.ID)
	}
	for _, env := range envs {
		envNames[env.ID] = env.Name
	}
	data, err := r.latestReleaseAvailability(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	data.envNames = envNames
	data.current = make(map[[2]int64]*ReleaseReference)
	for _, row := range rows {
		key := [2]int64{row.ProjectID, row.Deployment.EnvironmentID}
		if data.current[key] == nil && row.Deployment.Status == "succeeded" &&
			row.IsCheck == 0 {
			data.current[key] = &ReleaseReference{
				ID: row.Deployment.ReleaseID, Version: row.Version,
			}
		}
	}
	for _, p := range projects {
		result[p.ID] = data.project(
			p,
			envs,
			stagesByLifecycle[p.LifecycleID.Int64],
		)
	}
	return result, nil
}

func (r *Repository) latestReleaseAvailability(
	ctx context.Context,
	projectIDs []int64,
) (releaseAvailabilityData, error) {
	data := releaseAvailabilityData{
		latest:    make(map[int64]ReleaseReference),
		succeeded: make(map[[2]int64]bool),
	}
	for ids := range slices.Chunk(projectIDs, 500) {
		rows, err := r.Queries.ListProjectReleaseAvailability(ctx, ids)
		if err != nil {
			return data, err
		}
		for _, row := range rows {
			data.latest[row.ProjectID] = ReleaseReference{
				ID:      row.ID,
				Version: row.Version,
			}
			if row.EnvironmentID.Valid {
				data.succeeded[[2]int64{row.ProjectID, row.EnvironmentID.Int64}] = true
			}
		}
	}
	return data, nil
}

func (d releaseAvailabilityData) project(
	project db.Project,
	envs []db.Environment,
	stages []db.LifecycleStage,
) ProjectReleaseAvailability {
	summary := ProjectReleaseAvailability{
		Environments: []EnvironmentReleaseAvailability{},
	}
	latest := d.latest[project.ID]
	if latest.ID != 0 {
		summary.LatestRelease = &latest
	}
	for _, env := range envs {
		index := slices.IndexFunc(stages, func(s db.LifecycleStage) bool {
			return s.EnvironmentID == env.ID
		})
		if project.LifecycleID.Valid && index < 0 {
			continue
		}
		state := d.environment(project.ID, latest, env, stages, index)
		summary.UpdatesAvailable = summary.UpdatesAvailable ||
			state.UpdateAvailable
		summary.Environments = append(summary.Environments, state)
	}
	return summary
}

func (d releaseAvailabilityData) environment(
	projectID int64,
	latest ReleaseReference,
	env db.Environment,
	stages []db.LifecycleStage,
	index int,
) EnvironmentReleaseAvailability {
	state := EnvironmentReleaseAvailability{
		EnvironmentID: env.ID, EnvironmentName: env.Name,
		CurrentRelease: d.current[[2]int64{projectID, env.ID}],
	}
	if index >= 0 {
		state.RequiresApproval = stages[index].RequiresApproval != 0
	}
	state.UpdateAvailable = latest.ID != 0 &&
		(state.CurrentRelease == nil || state.CurrentRelease.ID != latest.ID)
	if !state.UpdateAvailable {
		return state
	}
	// Promotion readiness follows gate.Evaluate: the preceding stage needs a
	// successful real deployment of this release.
	state.ReadyToDeploy = true
	if index <= 0 {
		return state
	}
	previous := stages[index-1].EnvironmentID
	state.ReadyToDeploy = d.succeeded[[2]int64{projectID, previous}]
	if !state.ReadyToDeploy {
		state.Reason = fmt.Sprintf(
			"Waiting for successful deployment of %s to %s.",
			latest.Version,
			d.envNames[previous],
		)
	}
	return state
}
