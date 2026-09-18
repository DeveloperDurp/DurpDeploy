package api

import (
	"context"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type stepResponse struct {
	db.Step
	AgentSelectors []string `json:"agent_selectors"`
}

func validatePlacement(
	w http.ResponseWriter,
	r *http.Request,
	repo *repository.Repository,
	target string,
	selectors []string,
) (string, []string, bool) {
	available, err := repo.Queries.ListAvailableAgentLabels(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return "", nil, false
	}
	target, selectors, err = handler.ValidateStepPlacement(
		target,
		selectors,
		available,
	)
	if err != nil {
		RespondError(w, http.StatusBadRequest, err.Error())
		return "", nil, false
	}
	return target, selectors, true
}

type stepTemplateResponse struct {
	db.StepTemplate
	AgentSelectors []string `json:"agent_selectors"`
}

type stepTemplateVersionResponse struct {
	db.StepTemplateVersion
	AgentSelectors []string `json:"agent_selectors"`
}

func selectorList(selectors []string) []string {
	if selectors == nil {
		return []string{}
	}
	return selectors
}

func newStepResponse(
	ctx context.Context,
	repo *repository.Repository,
	step db.Step,
) (stepResponse, error) {
	selectors, err := repo.Queries.ListStepAgentSelectors(ctx, step.ID)
	if err != nil {
		return stepResponse{}, err
	}
	return stepResponse{
		Step:           step,
		AgentSelectors: selectorList(selectors),
	}, nil
}

func newStepResponses(
	ctx context.Context,
	repo *repository.Repository,
	steps []db.Step,
) ([]stepResponse, error) {
	responses := make([]stepResponse, len(steps))
	for index, step := range steps {
		response, err := newStepResponse(ctx, repo, step)
		if err != nil {
			return nil, err
		}
		responses[index] = response
	}
	return responses, nil
}

func newStepTemplateResponse(
	ctx context.Context,
	repo *repository.Repository,
	template db.StepTemplate,
) (stepTemplateResponse, error) {
	selectors, err := repo.Queries.ListTemplateAgentSelectors(ctx, template.ID)
	if err != nil {
		return stepTemplateResponse{}, err
	}
	return stepTemplateResponse{
		StepTemplate:   template,
		AgentSelectors: selectorList(selectors),
	}, nil
}

func newStepTemplateResponses(
	ctx context.Context,
	repo *repository.Repository,
	templates []db.StepTemplate,
) ([]stepTemplateResponse, error) {
	responses := make([]stepTemplateResponse, len(templates))
	for index, template := range templates {
		response, err := newStepTemplateResponse(ctx, repo, template)
		if err != nil {
			return nil, err
		}
		responses[index] = response
	}
	return responses, nil
}

func newStepTemplateVersionResponses(
	ctx context.Context,
	repo *repository.Repository,
	versions []db.StepTemplateVersion,
) ([]stepTemplateVersionResponse, error) {
	responses := make([]stepTemplateVersionResponse, len(versions))
	for index, version := range versions {
		selectors, err := repo.Queries.ListTemplateVersionAgentSelectors(
			ctx,
			version.ID,
		)
		if err != nil {
			return nil, err
		}
		responses[index] = stepTemplateVersionResponse{
			StepTemplateVersion: version,
			AgentSelectors:      selectorList(selectors),
		}
	}
	return responses, nil
}
