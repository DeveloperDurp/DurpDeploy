package pages

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestDeploymentLogPanelsKeepAmbiguousLegacyOutputSeparate(t *testing.T) {
	view := deploymentLogPanels(
		db.Release{
			StepsJson: `[{"name":"duplicate"},{"name":"duplicate"},{"name":"unique"}]`,
		},
		db.Deployment{Status: "failed"},
		[]db.DeploymentLog{
			{
				ID:       3,
				StepName: sql.NullString{String: "duplicate", Valid: true},
				Line:     "ambiguous",
			},
			{
				ID: 2,
				StepName: sql.NullString{
					String: "unique @ agent-1",
					Valid:  true,
				},
				Line: "legacy",
			},
			{
				ID:        1,
				StepIndex: sql.NullInt64{Int64: 1, Valid: true},
				Line:      "indexed",
				StepState: sql.NullString{String: "failed", Valid: true},
			},
		},
	)
	if view.LastID != 3 || view.Panels[0].State != "unknown" ||
		view.Panels[1].State != "failed" || view.Panels[1].Logs[0].Line != "indexed" ||
		view.Panels[2].State != "unknown" || view.Panels[2].Logs[0].Line != "legacy" ||
		view.Panels[3].Logs[0].Line != "ambiguous" {
		t.Fatalf("legacy/indexed grouping=%+v", view)
	}
}

func TestQueuedCancellationPanelsUseTrustedLifecycleReason(t *testing.T) {
	for _, scenario := range []struct {
		state, line, want string
	}{
		{"cancelled", repository.QueuedCancellationLogLine, "not_run_queued"},
		{"", repository.QueuedCancellationLogLine, "unknown"},
		{"cancelled", "Cancelled during execution.", "cancelled"},
		{"running", repository.QueuedCancellationLogLine, "unknown"},
	} {
		view := deploymentLogPanels(
			db.Release{StepsJson: `[{"name":"queued"}]`},
			db.Deployment{Status: "cancelled"},
			[]db.DeploymentLog{{
				ID: 1, StepIndex: sql.NullInt64{Valid: true}, Line: scenario.line,
				StepState: sql.NullString{
					String: scenario.state,
					Valid:  scenario.state != "",
				},
			}},
		)
		if view.Panels[0].State != scenario.want {
			t.Fatalf(
				"state=%q line=%q panel=%+v",
				scenario.state,
				scenario.line,
				view.Panels[0],
			)
		}
	}
}

func TestQueuedCancellationHistoricalPageKeepsReasonWithoutOutput(
	t *testing.T,
) {
	ctx := WithLogHistory(t.Context(), repository.LogPage{Before: 1},
		[]db.ListDeploymentLogStateMetadataRow{{
			ID: 3, StepIndex: sql.NullInt64{Valid: true},
			StepState: sql.NullString{String: "cancelled", Valid: true},
			Line:      repository.QueuedCancellationLogLine,
		}})
	view := deploymentPageLogView(ctx,
		db.Release{StepsJson: `[{"name":"queued"}]`},
		db.Deployment{Status: "cancelled"}, nil)
	panel := view.Panels[0]
	empty := deploymentStepEmptyMessage(panel.State)
	if panel.State != "not_run_queued" || len(panel.Logs) != 0 ||
		!view.Historical ||
		empty != "No output exists because this step never started." {
		t.Fatalf("historical cancellation panel=%+v", panel)
	}
}

func TestDeploymentLogPanelsRetainQuietActiveStep(t *testing.T) {
	view := deploymentLogPanels(
		db.Release{StepsJson: `[{"name":"quiet"}]`},
		db.Deployment{Status: "running"},
		[]db.DeploymentLog{
			{
				ID:        1,
				StepIndex: sql.NullInt64{Valid: true},
				StepState: sql.NullString{String: "running", Valid: true},
			},
		},
	)
	if view.Panels[0].Open ||
		deploymentActiveStepLabel(
			view,
			"running",
		) != "Running: Step 1 — quiet" {
		t.Fatalf("quiet active panel=%+v", view)
	}
}

func TestDeploymentLogPanelsStartCollapsed(t *testing.T) {
	for _, state := range []string{
		"pending", "running", "waiting", "failed", "succeeded",
		"cancelled", "not_run", "unknown",
	} {
		t.Run(state, func(t *testing.T) {
			status := state
			if state == "waiting" {
				status = "running"
			}
			view := deploymentLogPanels(
				db.Release{StepsJson: `[{"name":"Step"}]`},
				db.Deployment{Status: status},
				[]db.DeploymentLog{{
					ID:        1,
					StepIndex: sql.NullInt64{Valid: true},
					StepState: sql.NullString{String: state, Valid: true},
					Line:      "retained output",
				}},
			)
			panel := view.Panels[0]
			if panel.Open || panel.State != state ||
				len(
					panel.Logs,
				) != 1 || panel.Logs[0].Line != "retained output" {
				t.Fatalf("initial step panel=%+v", panel)
			}
		})
	}
}
