package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/views/pages"
)

func parseScheduleRouting(r *http.Request) (dispatch.Input, error) {
	input := dispatch.Input{
		Source: dispatch.SourceSchedule,
		Mode:   r.FormValue("target_mode"),
	}
	if input.Mode == "" {
		input.Mode = "inherit"
	}
	if value := r.FormValue("agent_label_id"); value != "" {
		labelID, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return dispatch.Input{}, dispatch.ErrInvalidPolicy
		}
		input.LabelID = labelID
	}
	if input.Mode == "label" {
		input.Strategy = r.FormValue("agent_strategy")
	}
	if _, err := dispatch.ParseInput(input); err != nil {
		return dispatch.Input{}, err
	}
	return input, nil
}

func (h *ScheduledDeploymentHandler) scheduleTargetOptions(
	ctx context.Context,
	scheduleID int64,
) (pages.ScheduleTargetOptions, error) {
	labels, err := h.repo.Queries.ListAgentLabels(ctx)
	if err != nil {
		return pages.ScheduleTargetOptions{}, fmt.Errorf("list agent labels: %w", err)
	}
	options := pages.ScheduleTargetOptions{Labels: labels, Mode: "inherit"}
	if scheduleID == 0 {
		return options, nil
	}
	policy, err := h.repo.Queries.GetScheduledDeploymentRoutingPolicy(
		ctx, scheduleID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return options, nil
	}
	if err != nil {
		return pages.ScheduleTargetOptions{}, fmt.Errorf(
			"get schedule routing policy: %w", err,
		)
	}
	options.Mode = policy.TargetMode
	options.LabelID = policy.AgentLabelID.Int64
	options.Strategy = policy.AgentStrategy.String
	return options, nil
}

func schedulePolicyParams(
	scheduleID int64,
	input dispatch.Input,
) db.CreateScheduledDeploymentRoutingPolicyParams {
	params := db.CreateScheduledDeploymentRoutingPolicyParams{
		ScheduledDeploymentID: scheduleID,
		TargetMode:            input.Mode,
	}
	if input.Mode == "label" {
		params.AgentLabelID = sql.NullInt64{Int64: input.LabelID, Valid: true}
		params.AgentStrategy = sql.NullString{String: input.Strategy, Valid: true}
	}
	return params
}
