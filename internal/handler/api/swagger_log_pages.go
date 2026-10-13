package api

import "database/sql"

// LogPage contains a bounded history window in increasing log-ID order.
// swagger:model LogPage
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLogPage struct {
	Items []swaggerLogPageEntry `json:"items"`
	// Exclusive cursor for the next older page. Zero means no older rows.
	NextBefore int64 `json:"next_before"`
	// Highest returned ID. Use this as the live stream's after cursor.
	LastID int64 `json:"last_id"`
	// Maximum number of returned rows (500).
	Limit int64 `json:"limit"`
	// Requested exclusive upper ID. Zero selects the latest page.
	Before int64 `json:"before"`
}

// LogPageEntry is a persisted log row, including nullable step metadata.
// swagger:model LogPageEntry
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLogPageEntry struct {
	ID           int64          `json:"id"`
	DeploymentID int64          `json:"deployment_id"`
	Line         string         `json:"line"`
	CreatedAt    int64          `json:"created_at"`
	StepIndex    sql.NullInt64  `json:"step_index"`
	StepName     sql.NullString `json:"step_name"`
	StepState    sql.NullString `json:"step_state"`
}

// swagger:parameters pageDeploymentLogs
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDeploymentLogPageParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// Return rows older than this ID. Omit to read the latest page.
	// in: query
	// minimum: 1
	Before int64 `json:"before"`
}

// swagger:parameters pageRunbookLogs exportRunbookLogs
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookLogPageParams struct {
	// in: path
	// required: true
	ID int64 `json:"id"`
	// in: path
	// required: true
	ExecutionID int64 `json:"executionId"`
}

// swagger:parameters pageRunbookLogs
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookLogCursorParams struct {
	// Return rows older than this ID. Omit to read the latest page.
	// in: query
	// minimum: 1
	Before int64 `json:"before"`
}
