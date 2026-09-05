package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/repository"
)

type ProjectExecutionPolicyState struct {
	Legacy     bool
	TargetMode string
	LabelID    int64
	LabelName  string
	Strategy   string
}

type ProjectExecutionPolicyInput struct {
	TargetMode string
	LabelID    int64
	Strategy   string
}

func LoadProjectExecutionPolicy(
	ctx context.Context,
	queries *db.Queries,
	projectID int64,
) (ProjectExecutionPolicyState, error) {
	stored, err := queries.GetProjectExecutionPolicy(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectExecutionPolicyState{Legacy: true}, nil
	}
	if err != nil {
		return ProjectExecutionPolicyState{}, fmt.Errorf(
			"get project execution policy: %w",
			err,
		)
	}
	state := ProjectExecutionPolicyState{TargetMode: stored.TargetMode}
	if stored.TargetMode == string(dispatch.TargetLocal) {
		return state, nil
	}
	if stored.TargetMode != string(dispatch.TargetLabel) ||
		!stored.AgentLabelID.Valid || !stored.AgentStrategy.Valid {
		return ProjectExecutionPolicyState{}, fmt.Errorf(
			"%w: stored project policy", dispatch.ErrInvalidPolicy,
		)
	}
	label, err := queries.GetAgentLabel(ctx, stored.AgentLabelID.Int64)
	if err != nil {
		return ProjectExecutionPolicyState{}, fmt.Errorf(
			"get project policy label: %w",
			err,
		)
	}
	state.LabelID = label.ID
	state.LabelName = label.Name
	state.Strategy = stored.AgentStrategy.String
	return state, nil
}

func ResolveProjectExecutionPolicy(
	ctx context.Context,
	repo *repository.Repository,
	input ProjectExecutionPolicyInput,
) (ProjectExecutionPolicyState, error) {
	policy, err := dispatch.NewResolver(repo).Resolve(
		ctx,
		0,
		0,
		dispatch.Input{
			Source:   dispatch.SourceRequest,
			Mode:     input.TargetMode,
			LabelID:  input.LabelID,
			Strategy: input.Strategy,
		},
	)
	if err != nil {
		return ProjectExecutionPolicyState{}, err
	}
	return ProjectExecutionPolicyState{
		TargetMode: string(policy.Mode),
		LabelID:    policy.LabelID,
		LabelName:  policy.LabelName,
		Strategy:   string(policy.Strategy),
	}, nil
}

func SaveProjectExecutionPolicy(
	ctx context.Context,
	queries *db.Queries,
	projectID int64,
	state ProjectExecutionPolicyState,
) error {
	labelID := sql.NullInt64{}
	strategy := sql.NullString{}
	if state.TargetMode == string(dispatch.TargetLabel) {
		labelID = sql.NullInt64{Int64: state.LabelID, Valid: true}
		strategy = sql.NullString{String: state.Strategy, Valid: true}
	}
	_, err := queries.GetProjectExecutionPolicy(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = queries.CreateProjectExecutionPolicy(
			ctx,
			db.CreateProjectExecutionPolicyParams{
				ProjectID: projectID, TargetMode: state.TargetMode,
				AgentLabelID: labelID, AgentStrategy: strategy,
			},
		)
		return err
	}
	if err != nil {
		return err
	}
	_, err = queries.UpdateProjectExecutionPolicy(
		ctx,
		db.UpdateProjectExecutionPolicyParams{
			ProjectID: projectID, TargetMode: state.TargetMode,
			AgentLabelID: labelID, AgentStrategy: strategy,
		},
	)
	return err
}
