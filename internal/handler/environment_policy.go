package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

const (
	executionModeLocal  = "local"
	executionModeRemote = "remote"
)

var (
	errEnvironmentPolicyMode = errors.New(
		"execution mode must be local or remote",
	)
	errEnvironmentPolicyPool = repository.ErrEnvironmentPolicyPool
	errEnvironmentPolicyTags = errors.New(
		"agent tags must be key=value pairs with unique keys",
	)
)

type environmentFormErrorResponse struct {
	Data    pages.EnvironmentFormData
	IsNew   bool
	Message string
}

func parseEnvironmentPolicy(
	r *http.Request,
) (pages.EnvironmentPolicyForm, error) {
	mode := strings.TrimSpace(r.FormValue("execution_mode"))
	policy := pages.EnvironmentPolicyForm{
		Remote:   mode == executionModeRemote,
		Selector: strings.TrimSpace(r.FormValue("agent_tags")),
	}
	switch mode {
	case "", executionModeLocal:
		return policy, nil
	case executionModeRemote:
		poolID, err := strconv.ParseInt(
			strings.TrimSpace(r.FormValue("pool_id")), 10, 64,
		)
		if err != nil || poolID < 1 {
			return policy, errEnvironmentPolicyPool
		}
		policy.PoolID = poolID
		selector, err := agentproto.ParseSelector(policy.Selector)
		if err != nil {
			return policy, errEnvironmentPolicyTags
		}
		policy.Selector = selector.String()
		return policy, nil
	default:
		return policy, errEnvironmentPolicyMode
	}
}

func (h *EnvironmentHandler) environmentFormData(
	ctx context.Context,
	environment *db.Environment,
) (pages.EnvironmentFormData, error) {
	pools, err := h.Repo.Queries.ListAgentPools(ctx)
	if err != nil {
		return pages.EnvironmentFormData{}, fmt.Errorf(
			"list agent pools: %w",
			err,
		)
	}
	data := pages.EnvironmentFormData{Environment: environment, Pools: pools}
	if environment.ID == 0 {
		return data, nil
	}
	policy, err := h.Repo.Queries.GetEnvironmentAgentPolicy(ctx, environment.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return data, nil
	}
	if err != nil {
		return pages.EnvironmentFormData{}, fmt.Errorf(
			"get environment policy: %w", err,
		)
	}
	data.Policy = pages.EnvironmentPolicyForm{
		Remote: true, PoolID: policy.PoolID, Selector: policy.Selector,
	}
	return data, nil
}

func (h *EnvironmentHandler) createEnvironmentWithPolicy(
	ctx context.Context,
	params db.CreateEnvironmentParams,
	policy pages.EnvironmentPolicyForm,
) error {
	return h.Repo.CreateEnvironmentWithPolicy(
		ctx,
		params,
		repository.EnvironmentPolicy{
			Remote: policy.Remote, PoolID: policy.PoolID, Selector: policy.Selector,
		},
	)
}

func (h *EnvironmentHandler) updateEnvironmentWithPolicy(
	ctx context.Context,
	params db.UpdateEnvironmentParams,
	policy pages.EnvironmentPolicyForm,
) error {
	return h.Repo.UpdateEnvironmentWithPolicy(
		ctx,
		params,
		repository.EnvironmentPolicy{
			Remote: policy.Remote, PoolID: policy.PoolID, Selector: policy.Selector,
		},
	)
}

func writeEnvironmentFormError(
	w http.ResponseWriter,
	r *http.Request,
	response environmentFormErrorResponse,
) {
	WriteFormError(
		w,
		r,
		pages.EnvironmentFormFragment(
			response.Data,
			response.IsNew,
			response.Message,
		),
		pages.EnvironmentForm(
			response.Data,
			response.IsNew,
			response.Message,
			r.URL.Path,
		),
	)
}

func isEnvironmentPolicyInputError(err error) bool {
	return errors.Is(err, errEnvironmentPolicyMode) ||
		errors.Is(err, errEnvironmentPolicyPool) ||
		errors.Is(err, errEnvironmentPolicyTags)
}
