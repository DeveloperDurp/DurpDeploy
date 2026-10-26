package api

import (
	"net/http"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/stepdefinition"
)

func prepareStepFields(
	w http.ResponseWriter,
	r *http.Request,
	repo *repository.Repository,
	step stepdefinition.Definition,
) (stepdefinition.Definition, bool) {
	step, err := handler.PrepareStepFields(
		step,
		repo.ResourceLimits().Execution,
	)
	if err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return step, false
	}
	target, selectors, ok := validatePlacement(w, r, repo,
		step.ExecutionTarget, step.AgentSelectors)
	if !ok {
		return step, false
	}
	step.ExecutionTarget, step.AgentSelectors = target, selectors
	if err := handler.ValidateStepExecutionFields(step); err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return step, false
	}
	return step, true
}
