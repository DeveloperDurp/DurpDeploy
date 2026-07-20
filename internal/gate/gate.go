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
	return evaluateStages(ctx, repo, release, environmentID, lc, stages)
}

// evaluateStages is the core of Evaluate, taking an already-loaded
// lifecycle and its stages so callers that need both the gate state and
// something else derived from stages (e.g. RequiresApproval) can fetch
// stages once and reuse them, instead of querying twice.
func evaluateStages(
	ctx context.Context,
	repo *repository.Repository,
	release db.Release,
	environmentID int64,
	lc db.Lifecycle,
	stages []db.LifecycleStage,
) (State, error) {
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

// RequiresApproval reports whether the lifecycle stage for environmentID
// has `requires_approval` set. A project without a lifecycle (or an
// environmentID outside the lifecycle's stages) never requires approval.
// Shared by the manual deploy handler and the scheduler so both paths
// enforce the same gate — a deployment created any other way must not be
// able to skip it.
func RequiresApproval(
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	environmentID int64,
) (bool, error) {
	if !project.LifecycleID.Valid {
		return false, nil
	}
	stages, err := repo.Queries.ListLifecycleStages(
		ctx,
		project.LifecycleID.Int64,
	)
	if err != nil {
		return false, err
	}
	return requiresApprovalStages(stages, environmentID), nil
}

func requiresApprovalStages(
	stages []db.LifecycleStage,
	environmentID int64,
) bool {
	for _, s := range stages {
		if s.EnvironmentID == environmentID {
			return s.RequiresApproval != 0
		}
	}
	return false
}

// CheckAndApproval combines Check and RequiresApproval for callers (the
// scheduler) that need both results for the same (project, environment)
// in one pass — it loads the lifecycle stages once instead of twice.
func CheckAndApproval(
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	release db.Release,
	environmentID int64,
) (blocked bool, reason string, requiresApproval bool, err error) {
	if !project.LifecycleID.Valid {
		return false, "", false, nil
	}

	lc, err := repo.Queries.GetLifecycle(ctx, project.LifecycleID.Int64)
	if err != nil {
		return false, "", false, err
	}
	stages, err := repo.Queries.ListLifecycleStages(ctx, lc.ID)
	if err != nil {
		return false, "", false, err
	}

	state, err := evaluateStages(ctx, repo, release, environmentID, lc, stages)
	if err != nil {
		return false, "", false, err
	}
	if !state.Deployable {
		return true, state.Reason, false, nil
	}
	return false, "", requiresApprovalStages(stages, environmentID), nil
}
