package pages

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
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
	if view.LastID != 3 || view.Panels[0].State != "not_run" ||
		view.Panels[1].State != "failed" || view.Panels[1].Logs[0].Line != "indexed" ||
		view.Panels[2].State != "unknown" || view.Panels[2].Logs[0].Line != "legacy" ||
		view.Panels[3].Logs[0].Line != "ambiguous" {
		t.Fatalf("legacy/indexed grouping=%+v", view)
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
	if !view.Panels[0].Open ||
		deploymentActiveStepLabel(
			view,
			"running",
		) != "Running: Step 1 — quiet" {
		t.Fatalf("quiet active panel=%+v", view)
	}
}
