package repository

import (
	"context"
	"database/sql"
	"strings"

	"durpdeploy/internal/db"
)

func (r *Repository) SetStepPlacement(
	ctx context.Context,
	stepID int64,
	target string,
	selectors []string,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		return setStepPlacement(ctx, q, stepID, target, selectors)
	})
}

func (r *Repository) CreateStepWithPlacement(
	ctx context.Context,
	params db.CreateStepParams,
	target string,
	selectors []string,
) (db.Step, error) {
	var step db.Step
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		step, err = q.CreateStep(ctx, params)
		if err != nil {
			return err
		}
		return setStepPlacement(ctx, q, step.ID, target, selectors)
	})
	step.ExecutionTarget = target
	return step, err
}

func (r *Repository) UpdateStepWithPlacement(
	ctx context.Context,
	params db.UpdateStepParams,
	target string,
	selectors []string,
) (db.Step, error) {
	var step db.Step
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		step, err = q.UpdateStep(ctx, params)
		if err != nil {
			return err
		}
		return setStepPlacement(ctx, q, step.ID, target, selectors)
	})
	step.ExecutionTarget = target
	return step, err
}

func setStepPlacement(
	ctx context.Context,
	q *db.Queries,
	stepID int64,
	target string,
	selectors []string,
) error {
	changed, err := q.SetStepExecutionTarget(
		ctx,
		db.SetStepExecutionTargetParams{
			ExecutionTarget: target,
			ID:              stepID,
		},
	)
	if err != nil {
		return err
	}
	if changed != 1 {
		return sql.ErrNoRows
	}
	if err := q.DeleteStepAgentSelectors(ctx, stepID); err != nil {
		return err
	}
	if target != "agent" {
		return nil
	}
	for _, selector := range selectors {
		if err := q.AddStepAgentSelector(
			ctx,
			db.AddStepAgentSelectorParams{
				StepID: stepID,
				Label:  strings.ToLower(strings.TrimSpace(selector)),
			},
		); err != nil {
			return err
		}
	}
	return nil
}
