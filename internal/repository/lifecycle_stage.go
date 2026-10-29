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
	ErrLifecycleStageIDsIncomplete = errors.New(
		"stage_ids must include every stage exactly once",
	)
	ErrLifecycleStageUnknown = errors.New(
		"stage_ids contains an unknown stage",
	)
	ErrLifecycleStageDuplicate = errors.New("stage_ids contains duplicates")
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

// MoveLifecycleStage swaps adjacent positions without changing stage metadata.
func (r *Repository) MoveLifecycleStage(
	ctx context.Context,
	lifecycleID, stageID int64,
	moveUp bool,
) error {
	_, err := r.reorderLifecycleStages(ctx, lifecycleID,
		func(stages []db.LifecycleStage) ([]db.LifecycleStage, error) {
			for i, stage := range stages {
				if stage.ID != stageID {
					continue
				}
				neighbor := i + 1
				if moveUp {
					neighbor = i - 1
				}
				if neighbor < 0 || neighbor >= len(stages) {
					return nil, nil
				}
				other := stages[neighbor]
				stage.SortOrder, other.SortOrder = other.SortOrder, stage.SortOrder
				return []db.LifecycleStage{stage, other}, nil
			}
			return nil, sql.ErrNoRows
		})
	return err
}

// ReorderLifecycleStages assigns consecutive positions to a complete permutation.
func (r *Repository) ReorderLifecycleStages(
	ctx context.Context,
	lifecycleID int64,
	stageIDs []int64,
) ([]db.LifecycleStage, error) {
	return r.reorderLifecycleStages(ctx, lifecycleID,
		func(stages []db.LifecycleStage) ([]db.LifecycleStage, error) {
			if len(stageIDs) != len(stages) {
				return nil, ErrLifecycleStageIDsIncomplete
			}
			existing := make(map[int64]db.LifecycleStage, len(stages))
			for _, stage := range stages {
				existing[stage.ID] = stage
			}
			seen := make(map[int64]bool, len(stageIDs))
			ordered := make([]db.LifecycleStage, len(stageIDs))
			for i, id := range stageIDs {
				stage, ok := existing[id]
				if !ok {
					return nil, ErrLifecycleStageUnknown
				}
				if seen[id] {
					return nil, ErrLifecycleStageDuplicate
				}
				seen[id] = true
				stage.SortOrder = int64(i + 1)
				ordered[i] = stage
			}
			return ordered, nil
		})
}

func (r *Repository) reorderLifecycleStages(
	ctx context.Context,
	lifecycleID int64,
	plan func([]db.LifecycleStage) ([]db.LifecycleStage, error),
) ([]db.LifecycleStage, error) {
	return withSQLiteBusyRetryValue(ctx, func() ([]db.LifecycleStage, error) {
		var result []db.LifecycleStage
		err := r.WithTx(ctx, func(q *db.Queries) error {
			locked, err := q.LockLifecycle(ctx, lifecycleID)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			stages, err := q.ListLifecycleStages(ctx, lifecycleID)
			if err != nil {
				return err
			}
			changes, err := plan(stages)
			if err != nil {
				return err
			}
			if err := updateLifecycleStageOrders(
				ctx, q, lifecycleID, stages, changes,
			); err != nil {
				return err
			}
			result, err = q.ListLifecycleStages(ctx, lifecycleID)
			return err
		})
		return result, err
	})
}

func updateLifecycleStageOrders(
	ctx context.Context,
	q *db.Queries,
	lifecycleID int64,
	stages, changes []db.LifecycleStage,
) error {
	update := func(id, position int64) error {
		rows, err := q.UpdateLifecycleStageOrder(ctx,
			db.UpdateLifecycleStageOrderParams{
				ID: id, LifecycleID: lifecycleID, SortOrder: position,
			})
		if err == nil && rows != 1 {
			return sql.ErrNoRows
		}
		return err
	}
	used := make(map[int64]bool, len(stages))
	for _, stage := range stages {
		used[stage.SortOrder] = true
	}
	// Reserve unused negatives before writing final unique positions.
	temporary := int64(-1)
	for _, stage := range changes {
		for used[temporary] {
			if temporary == math.MinInt64 {
				return ErrLifecycleSortOrderConflict
			}
			temporary--
		}
		if err := update(stage.ID, temporary); err != nil {
			return err
		}
		used[temporary] = true
	}
	for _, stage := range changes {
		if err := update(stage.ID, stage.SortOrder); err != nil {
			return err
		}
	}
	return nil
}
