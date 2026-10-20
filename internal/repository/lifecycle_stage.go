package repository

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"durpdeploy/internal/db"
)

var (
	ErrLifecycleEnvironmentExists = errors.New(
		"environment_id is already in this lifecycle",
	)
	ErrLifecycleSortOrderConflict = errors.New(
		"sort_order is occupied or has no append position",
	)
)

// CreateLifecycleStage preserves positive positions and appends otherwise.
func (r *Repository) CreateLifecycleStage(
	ctx context.Context,
	params db.CreateLifecycleStageParams,
) (db.LifecycleStage, error) {
	return withSQLiteBusyRetryValue(ctx, func() (db.LifecycleStage, error) {
		var stage db.LifecycleStage
		err := r.WithTx(ctx, func(q *db.Queries) error {
			// Lock before reading so concurrent appends allocate distinct positions.
			locked, err := q.LockLifecycle(ctx, params.LifecycleID)
			if err != nil {
				return err
			}
			if locked == 0 {
				return sql.ErrNoRows
			}
			stages, err := q.ListLifecycleStages(ctx, params.LifecycleID)
			if err != nil {
				return err
			}
			position, err := lifecycleStagePosition(params, stages)
			if err != nil {
				return err
			}
			create := params
			create.SortOrder = position
			stage, err = q.CreateLifecycleStage(ctx, create)
			return err
		})
		return stage, err
	})
}

func lifecycleStagePosition(
	params db.CreateLifecycleStageParams,
	stages []db.LifecycleStage,
) (int64, error) {
	next := int64(1)
	for _, existing := range stages {
		if existing.EnvironmentID == params.EnvironmentID {
			return 0, ErrLifecycleEnvironmentExists
		}
		if params.SortOrder > 0 && existing.SortOrder == params.SortOrder {
			return 0, ErrLifecycleSortOrderConflict
		}
		if params.SortOrder <= 0 && existing.SortOrder >= next {
			if existing.SortOrder == math.MaxInt64 {
				return 0, ErrLifecycleSortOrderConflict
			}
			next = existing.SortOrder + 1
		}
	}
	if params.SortOrder > 0 {
		return params.SortOrder, nil
	}
	return next, nil
}
