package gate

import (
	"context"
	"database/sql"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// State describes, for a given (project, release, env) triple, whether a
// deploy is allowed and, if not, why.
type State struct {
	Deployable bool
	Reason     string
	Bypassable bool // true = user can force=true to override
}

// Evaluate returns the gate state for a single env. Pure function: no
// receiver, no I/O outside the repository.
func Evaluate(
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	release db.Release,
	environmentID int64,
) (State, error) {
	if !project.LifecycleID.Valid {
		return State{Deployable: true}, nil
	}

	lc, err := repo.Queries.GetLifecycle(ctx, project.LifecycleID.Int64)
	if err != nil {
		return State{}, err
	}
	stages, err := repo.Queries.ListLifecycleStages(ctx, lc.ID)
	if err != nil {
		return State{}, err
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
		return State{
			Deployable: false,
			Reason: fmt.Sprintf(
				"%s is not part of the lifecycle %q. Projects with a lifecycle can only deploy to their lifecycle stages.",
				envName,
				lc.Name,
			),
			Bypassable: false,
		}, nil
	}
	if idx == 0 {
		return State{Deployable: true}, nil
	}

	prev := stages[idx-1]
	dep, err := repo.Queries.GetLatestSuccessfulDeploymentForReleaseEnv(
		ctx,
		db.GetLatestSuccessfulDeploymentForReleaseEnvParams{
			ReleaseID:     release.ID,
			EnvironmentID: prev.EnvironmentID,
		},
	)
	if err != nil && err != sql.ErrNoRows {
		return State{}, err
	}
	if err == sql.ErrNoRows || dep.ReleaseID == 0 {
		prevEnv, _ := repo.Queries.GetEnvironment(ctx, prev.EnvironmentID)
		prevName := "(unknown)"
		if prevEnv.ID != 0 {
			prevName = prevEnv.Name
		}
		return State{
			Deployable: false,
			Reason: fmt.Sprintf(
				"%s has not been successfully deployed to %s yet. Tick Force to deploy anyway.",
				release.Version,
				prevName,
			),
			Bypassable: true,
		}, nil
	}
	return State{Deployable: true}, nil
}

// Check returns whether the deployment is blocked and the reason.
// Exported for the scheduler; handlers that need Bypassable should use Evaluate.
func Check(
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	release db.Release,
	environmentID int64,
) (blocked bool, reason string) {
	state, err := Evaluate(ctx, repo, project, release, environmentID)
	if err != nil {
		return true, err.Error()
	}
	if !state.Deployable {
		return true, state.Reason
	}
	return false, ""
}
