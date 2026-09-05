package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentExecutionTarget_RejectsCrossProjectRelease(t *testing.T) {
	fixture := newRoutingFixture(t, 0)
	other, err := fixture.repo.Queries.CreateProject(
		context.Background(), db.CreateProjectParams{Name: "other"},
	)
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	service := NewCreationService(
		fixture.repo,
		New(fixture.repo, fixture.box, nil),
	)
	_, err = service.Create(context.Background(), CreateRequest{
		ProjectID: other.ID, ReleaseID: fixture.release.ID,
		EnvironmentID: fixture.environment.ID,
		Routing:       Input{Source: SourceRequest, Mode: "local"},
	})
	if !errors.Is(err, ErrReleaseProjectMismatch) {
		t.Fatalf("Create() error = %v, want %v", err, ErrReleaseProjectMismatch)
	}
}

func TestDeploymentExecutionTarget_SelectsDefaultLocalAndRoundRobin(
	t *testing.T,
) {
	tests := []struct {
		name, mode, strategy, wantMode, wantAgent string
	}{
		{
			name:     "default uses project local",
			mode:     "default",
			wantMode: "local",
		},
		{name: "explicit local", mode: "local", wantMode: "local"},
		{
			name:      "label round robin",
			mode:      "label",
			strategy:  "round_robin",
			wantMode:  "remote",
			wantAgent: "agent-a",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRoutingFixture(t, 2)
			if _, err := fixture.repo.Queries.CreateProjectExecutionPolicy(
				context.Background(), db.CreateProjectExecutionPolicyParams{
					ProjectID: fixture.project.ID, TargetMode: "local",
				},
			); err != nil {
				t.Fatalf("create project policy: %v", err)
			}
			labelID := int64(0)
			if test.mode == "label" {
				labelID = fixture.label.ID
			}
			service := NewCreationService(
				fixture.repo,
				New(fixture.repo, fixture.box, nil),
			)
			deployment, err := service.Create(
				context.Background(),
				CreateRequest{
					ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
					EnvironmentID: fixture.environment.ID,
					Routing: Input{
						Source:   SourceRequest,
						Mode:     test.mode,
						LabelID:  labelID,
						Strategy: test.strategy,
					},
				},
			)
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			dispatch, err := fixture.repo.Queries.GetDeploymentDispatch(
				context.Background(),
				deployment.ID,
			)
			if err != nil {
				t.Fatalf("get dispatch: %v", err)
			}
			if dispatch.Mode != test.wantMode ||
				dispatch.AssignedAgentID.String != test.wantAgent {
				t.Fatalf("dispatch = %#v", dispatch)
			}
		})
	}
}

func TestDeploymentExecutionTarget_RejectsMalformedTargetFields(t *testing.T) {
	tests := []Input{
		{Source: SourceRequest, Mode: "remote"},
		{Source: SourceRequest, Mode: "label", Strategy: "all"},
		{Source: SourceRequest, Mode: "label", LabelID: 1},
		{Source: SourceRequest, Mode: "label", LabelID: 1, Strategy: "random"},
	}
	for _, input := range tests {
		_, err := ParseInput(input)
		if !errors.Is(err, ErrInvalidPolicy) {
			t.Errorf("ParseInput(%#v) error = %v", input, err)
		}
	}
}

func TestDeploymentRoutingAtomicity_PayloadFailureRollsBackRootAndCursor(
	t *testing.T,
) {
	fixture := newRoutingFixture(t, 1)
	service := NewCreationService(fixture.repo, New(fixture.repo, nil, nil))
	_, err := service.Create(context.Background(), CreateRequest{
		ProjectID: fixture.project.ID, ReleaseID: fixture.release.ID,
		EnvironmentID: fixture.environment.ID,
		Routing: Input{
			Source:   SourceRequest,
			Mode:     "label",
			LabelID:  fixture.label.ID,
			Strategy: "round_robin",
		},
	})
	if err == nil {
		t.Fatal("Create() error = nil, want payload preparation failure")
	}
	deployments, listErr := fixture.repo.Queries.ListDeployments(
		context.Background(),
	)
	if listErr != nil || len(deployments) != 0 {
		t.Fatalf(
			"deployments = %d, error = %v; want none",
			len(deployments),
			listErr,
		)
	}
	if _, cursorErr := fixture.repo.Queries.GetAgentLabelCursor(
		context.Background(), fixture.label.ID,
	); !errors.Is(cursorErr, sql.ErrNoRows) {
		t.Fatalf("cursor error = %v, want sql.ErrNoRows", cursorErr)
	}
}
