package repository

import (
	"context"
	"errors"
	"slices"

	"durpdeploy/internal/db"
)

const LogPageLimit int64 = 500

// LogPage uses immutable row IDs. Items run from oldest to newest in each page.
type LogPage struct {
	Items      []db.DeploymentLog `json:"items"`
	NextBefore int64              `json:"next_before"`
	LastID     int64              `json:"last_id"`
	Limit      int64              `json:"limit"`
	Before     int64              `json:"before"`
}

func (r *Repository) ReadLogPage(
	ctx context.Context,
	deploymentID, before int64,
) (LogPage, error) {
	if before < 0 {
		return LogPage{}, errors.New("log cursor must be non-negative")
	}
	rows, err := r.Queries.ListDeploymentLogsBefore(ctx,
		db.ListDeploymentLogsBeforeParams{
			DeploymentID: deploymentID, BeforeID: before,
			PageLimit: LogPageLimit + 1,
		})
	if err != nil {
		return LogPage{}, err
	}
	page := LogPage{Limit: LogPageLimit, Before: before}
	if len(rows) > int(LogPageLimit) {
		rows = rows[:LogPageLimit]
		page.NextBefore = rows[len(rows)-1].ID
	}
	slices.Reverse(rows)
	page.Items = rows
	if page.Items == nil {
		page.Items = []db.DeploymentLog{}
	}
	if len(rows) != 0 {
		page.LastID = rows[len(rows)-1].ID
	}
	return page, nil
}
