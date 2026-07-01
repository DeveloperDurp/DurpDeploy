package handler

import (
	"context"
	"database/sql"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// gateState describes, for a given (project, release, env) triple, whether a
// deploy is allowed and, if not, why. Used by the releases page to decide which
// envs to show in the dropdown and to render a tooltip explaining the gate.
type gateState struct {
	deployable bool
	reason     string
	bypassable bool // true = user can force=true to override
}

// evaluateGate returns the gate state for a single env. Pure function: no
// receiver, no I/O outside the repository. Used both by the deploy handler
// (block on violation) and the releases page (filter the dropdown).
func evaluateGate(ctx context.Context, repo *repository.Repository, project db.Project, release db.Release, environmentID int64) (gateState, error) {
	if !project.LifecycleID.Valid {
		return gateState{deployable: true}, nil
	}

	lc, err := repo.Queries.GetLifecycle(ctx, project.LifecycleID.Int64)
	if err != nil {
		return gateState{}, err
	}
	stages, err := repo.Queries.ListLifecycleStages(ctx, lc.ID)
	if err != nil {
		return gateState{}, err
	}

	idx := -1
	for i, s := range stages {
		if s.EnvironmentID == environmentID {
			idx = i
			break
		}
	}
	if idx < 0 {
		env, _ := repo.Queries.GetEnvironment(ctx, environmentID)
		envName := "(unknown)"
		if env.ID != 0 {
			envName = env.Name
		}
		return gateState{
			deployable: false,
			reason:     fmt.Sprintf("%s is not part of the lifecycle %q. Projects with a lifecycle can only deploy to their lifecycle stages.", envName, lc.Name),
			bypassable: false,
		}, nil
	}
	if idx == 0 {
		return gateState{deployable: true}, nil
	}

	prev := stages[idx-1]
	dep, err := repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(ctx, db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
		ReleaseID:     release.ID,
		EnvironmentID: prev.EnvironmentID,
	})
	if err != nil && err != sql.ErrNoRows {
		return gateState{}, err
	}
	if err == sql.ErrNoRows || dep.ReleaseID == 0 {
		prevEnv, _ := repo.Queries.GetEnvironment(ctx, prev.EnvironmentID)
		prevName := "(unknown)"
		if prevEnv.ID != 0 {
			prevName = prevEnv.Name
		}
		return gateState{
			deployable: false,
			reason:     fmt.Sprintf("%s has not been successfully deployed to %s yet. Tick Force to deploy anyway.", release.Version, prevName),
			bypassable: true,
		}, nil
	}
	return gateState{deployable: true}, nil
}

// availableEnvsForRelease returns one entry per env the project is allowed to
// consider, each with its current gate state. Used by the releases page to
// populate the deploy dropdown.
type availableEnv struct {
	Environment db.Environment
	State       gateState
}

func availableEnvsForRelease(ctx context.Context, repo *repository.Repository, project db.Project, release db.Release) ([]availableEnv, error) {
	allEnvs, err := repo.Queries.ListEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	if !project.LifecycleID.Valid {
		// Free-floating project: every env is deployable, no gate to evaluate.
		out := make([]availableEnv, len(allEnvs))
		for i, e := range allEnvs {
			out[i] = availableEnv{Environment: e, State: gateState{deployable: true}}
		}
		return out, nil
	}
	stageIDs, err := repo.Queries.ListLifecycleStageEnvironmentIDs(ctx, project.LifecycleID.Int64)
	if err != nil {
		return nil, err
	}
	idSet := make(map[int64]bool, len(stageIDs))
	for _, id := range stageIDs {
		idSet[id] = true
	}
	out := make([]availableEnv, 0, len(stageIDs))
	for _, e := range allEnvs {
		if !idSet[e.ID] {
			continue
		}
		state, err := evaluateGate(ctx, repo, project, release, e.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, availableEnv{Environment: e, State: state})
	}
	return out, nil
}
