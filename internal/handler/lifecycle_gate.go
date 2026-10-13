package handler

import (
	"context"

	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
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
func evaluateGate(
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	release db.Release,
	environmentID int64,
) (gateState, error) {
	state, err := gate.Evaluate(
		ctx,
		repo.Queries,
		project,
		release,
		environmentID,
	)
	if err != nil {
		return gateState{}, err
	}
	return gateState{
		deployable: state.Deployable,
		reason:     state.Reason,
		bypassable: state.Bypassable,
	}, nil
}

// EvaluateGate returns the gate state for a single env. Exported so the
// scheduler (and any other caller) can reuse the same logic.
func EvaluateGate(
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	release db.Release,
	environmentID int64,
) (gateState, error) {
	return evaluateGate(ctx, repo, project, release, environmentID)
}

// CheckPromotionGate returns whether the deployment is blocked and the reason.
// Exported for the scheduler; handlers that need bypassable should use EvaluateGate.
func CheckPromotionGate(
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	release db.Release,
	environmentID int64,
) (blocked bool, reason string) {
	return gate.Check(ctx, repo.Queries, project, release, environmentID)
}
