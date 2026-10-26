package repository

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"

	"durpdeploy/internal/db"
)

const LogPageLimit int64 = 500

// LogPage uses immutable row IDs. Items run from oldest to newest in each page.
type LogPage struct {
	Items         []LogPreview `json:"items"`
	NextBefore    int64        `json:"next_before"`
	LastID        int64        `json:"last_id"`
	Limit         int64        `json:"limit"`
	Before        int64        `json:"before"`
	ByteLimit     int          `json:"byte_limit"`
	LineByteLimit int          `json:"line_byte_limit"`
}

func (page LogPage) Logs() []db.DeploymentLog {
	logs := make([]db.DeploymentLog, len(page.Items))
	for i, row := range page.Items {
		logs[i] = row.DeploymentLog
	}
	return logs
}

func (r *Repository) ReadLogPage(
	ctx context.Context,
	deploymentID, before int64,
) (LogPage, error) {
	if before < 0 {
		return LogPage{}, errors.New("log cursor must be non-negative")
	}
	through := int64(math.MaxInt64)
	if before > 0 {
		through = before - 1
	}
	limits := r.ResourceLimits().Logs
	page := LogPage{Limit: limits.PageRows, Before: before,
		ByteLimit: int(limits.PageBytes), LineByteLimit: int(limits.LineBytes),
		Items: []LogPreview{}}
	// Reserve space for the JSON envelope, counters, and final newline.
	remaining := int(limits.PageBytes) - 1024
readPages:
	for {
		rows, err := r.Queries.ListDeploymentLogsBefore(ctx,
			db.ListDeploymentLogsBeforeParams{
				DeploymentID: deploymentID, ThroughID: through,
				PageLimit:    limits.PreviewBatch,
				PreviewLimit: limits.LineBytes + 1,
			})
		if err != nil {
			return LogPage{}, err
		}
		for _, row := range rows {
			log := logPreviewFromRow(db.ListDeploymentLogPreviewsAfterRow(row), int(limits.LineBytes))
			encoded, err := json.Marshal(log)
			if err != nil {
				return LogPage{}, err
			}
			if int64(len(page.Items)) == limits.PageRows || len(encoded)+1 > remaining {
				page.NextBefore = page.Items[len(page.Items)-1].ID
				break readPages
			}
			remaining -= len(encoded) + 1
			page.Items = append(page.Items, log)
		}
		if int64(len(rows)) < limits.PreviewBatch {
			break
		}
		through = rows[len(rows)-1].ID - 1
	}
	slices.Reverse(page.Items)
	if len(page.Items) != 0 {
		page.LastID = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}
