package repository

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"

	"durpdeploy/internal/db"
)

const (
	LogPageByteLimit     = 1 << 20
	LogLineByteLimit     = 16 << 10
	LogPreviewBatchLimit = 64
	logPreviewSuffix     = " … [truncated; export all logs for complete output]"
)

// LogPreview preserves row identity while bounding its display text.
// Persisted rows and complete exports are never changed.
type LogPreview struct {
	db.DeploymentLog
	Truncated bool `json:"truncated,omitempty"`
}

func previewText(value string, limit int) (string, bool) {
	if len(value) <= limit && utf8.ValidString(value) {
		return value, false
	}
	// Repair invalid text before applying the byte cap, including sliced runes.
	text := strings.ToValidUTF8(value, "�")
	end := min(len(text), limit-len(logPreviewSuffix))
	for end > 0 && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + logPreviewSuffix, true
}

func logPreviewFromRow(
	row db.ListDeploymentLogPreviewsAfterRow,
	limit int,
) LogPreview {
	line, shortened := previewText(row.Line, limit)
	name, nameShortened := previewText(row.StepName, limit)
	state, stateShortened := previewText(row.StepState, limit)
	return LogPreview{
		DeploymentLog: db.DeploymentLog{
			ID: row.ID, DeploymentID: row.DeploymentID,
			Line: line, CreatedAt: row.CreatedAt, StepIndex: row.StepIndex,
			StepName: sql.NullString{
				String: name, Valid: row.StepNameValid != 0,
			},
			StepState: sql.NullString{
				String: state, Valid: row.StepStateValid != 0,
			},
		},
		Truncated: shortened || nameShortened || stateShortened,
	}
}

func (r *Repository) ReadLogPreviewsAfter(
	ctx context.Context, deploymentID, after int64,
) ([]LogPreview, error) {
	limits := r.ResourceLimits().Logs
	rows, err := r.Queries.ListDeploymentLogPreviewsAfter(ctx,
		db.ListDeploymentLogPreviewsAfterParams{
			DeploymentID: deploymentID, AfterID: after,
			PreviewLimit: limits.LineBytes + 1,
			BatchLimit:   limits.PreviewBatch,
		})
	if err != nil {
		return nil, err
	}
	logs := make([]LogPreview, len(rows))
	for i, row := range rows {
		logs[i] = logPreviewFromRow(row, int(limits.LineBytes))
	}
	return logs, nil
}
