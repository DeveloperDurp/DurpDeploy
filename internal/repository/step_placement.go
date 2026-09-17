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
	label string,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
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
		err = q.AddStepAgentSelector(
			ctx,
			db.AddStepAgentSelectorParams{
				StepID: stepID,
				Label:  strings.ToLower(strings.TrimSpace(label)),
			},
		)
		return err
	})
}

func (r *Repository) CreateStepWithPlacement(
	ctx context.Context,
	params db.CreateStepParams,
	target string,
	label string,
) (db.Step, error) {
	var step db.Step
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		step, err = q.CreateStep(ctx, params)
		if err != nil {
			return err
		}
		return setStepPlacement(ctx, q, step.ID, target, label)
	})
	return step, err
}

func (r *Repository) UpdateStepWithPlacement(
	ctx context.Context,
	params db.UpdateStepParams,
	target string,
	label string,
) (db.Step, error) {
	var step db.Step
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		step, err = q.UpdateStep(ctx, params)
		if err != nil {
			return err
		}
		return setStepPlacement(ctx, q, step.ID, target, label)
	})
	return step, err
}

func setStepPlacement(
	ctx context.Context,
	q *db.Queries,
	stepID int64,
	target string,
	label string,
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
	err = q.AddStepAgentSelector(
		ctx,
		db.AddStepAgentSelectorParams{
			StepID: stepID,
			Label:  strings.ToLower(strings.TrimSpace(label)),
		},
	)
	return err
}
