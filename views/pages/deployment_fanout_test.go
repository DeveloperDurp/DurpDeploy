package pages

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/deploymentstate"
)

func TestDeploymentFanoutView_ParentLinksWithoutStream(t *testing.T) {
	// Given
	request := auth.SetUser(
		httptest.NewRequest("GET", "/", nil),
		&db.User{Role: "viewer"},
	)
	routing := deploymentstate.Dispatch{
		Mode:            "fanout",
		Source:          "request",
		TargetMode:      "label",
		AgentLabelName:  "Frozen label",
		AgentStrategy:   "all",
		AggregateStatus: "running",
		Children: []deploymentstate.Child{
			{
				ID:        2,
				Status:    "running",
				DetailURL: "/deployments/2",
				Dispatch: deploymentstate.Dispatch{
					Mode:  "remote",
					State: "started",
					Agent: &deploymentstate.Agent{
						ID:     "copied-id",
						Name:   "Copied name",
						Status: "deleted",
					},
				},
			},
		},
	}
	// When
	markup := renderAgentAdminPage(
		t,
		request.Context(),
		DeploymentDetail(
			db.Project{},
			db.Release{StepsJson: "[]"},
			db.Environment{},
			db.Deployment{ID: 1, Status: "running"},
			routing,
			nil,
		),
	)
	// Then
	for _, want := range []string{`data-fanout-children`, `href="/deployments/2"`, `href="/deployments/2#logs"`, "Frozen label", "Copied name", "copied-id", "deleted"} {
		if !strings.Contains(markup, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, forbidden := range []string{"new EventSource", `href="/deployments/1/logs.txt"`, "hx-post="} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("parent viewer contains %s", forbidden)
		}
	}
}

func TestDeploymentFanoutView_ChildHasLogsButNoRootActions(t *testing.T) {
	// Given
	request := auth.SetUser(
		httptest.NewRequest("GET", "/", nil),
		&db.User{Role: "admin"},
	)
	child := db.Deployment{
		ID:                 2,
		Status:             "failed",
		ParentDeploymentID: sql.NullInt64{Int64: 1, Valid: true},
	}
	// When
	markup := renderAgentAdminPage(
		t,
		request.Context(),
		DeploymentDetail(
			db.Project{},
			db.Release{StepsJson: "[]"},
			db.Environment{},
			child,
			deploymentstate.Dispatch{Mode: "remote", State: "failed"},
			nil,
		),
	)
	// Then
	if !strings.Contains(markup, "new EventSource") ||
		strings.Contains(markup, "hx-post=") ||
		!strings.Contains(markup, `href="/deployments/1"`) {
		t.Fatal("child log or root action contract failed")
	}
}

func TestDeploymentFanoutView_FailedParentHasNoApprovalPromise(t *testing.T) {
	// Given
	request := httptest.NewRequest("GET", "/", nil)
	// When
	markup := renderAgentAdminPage(
		t,
		request.Context(),
		DeploymentChildren(db.Deployment{ID: 1, Status: "failed"}, nil),
	)
	// Then
	if strings.Contains(markup, "approved") ||
		strings.Contains(markup, "hx-trigger=") {
		t.Fatal("failed parent offers future approval or keeps polling")
	}
}
