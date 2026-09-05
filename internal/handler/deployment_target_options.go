package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

func loadExecutionTargetOptions(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
) (pages.ExecutionTargetOptions, error) {
	labels, err := repo.Queries.ListAgentLabels(ctx)
	if err != nil {
		return pages.ExecutionTargetOptions{}, fmt.Errorf(
			"list agent labels: %w",
			err,
		)
	}
	options := pages.ExecutionTargetOptions{
		Default: "legacy environment assignment, otherwise local",
		Labels:  labels,
	}
	policy, err := repo.Queries.GetProjectExecutionPolicy(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return options, nil
	}
	if err != nil {
		return pages.ExecutionTargetOptions{}, fmt.Errorf(
			"get project execution policy: %w", err,
		)
	}
	if policy.TargetMode == "local" {
		options.Default = "local"
		return options, nil
	}
	for _, label := range labels {
		if policy.AgentLabelID.Valid && label.ID == policy.AgentLabelID.Int64 {
			options.Default = fmt.Sprintf(
				"%s (%s)", label.Name, policy.AgentStrategy.String,
			)
			break
		}
	}
	return options, nil
}
