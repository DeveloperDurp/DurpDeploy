package dispatch

import (
	"context"
	"database/sql"
	"durpdeploy/internal/db"
	"errors"
	"testing"
)

func TestRoutingPolicy_ParsesApprovedEnums(t *testing.T) {
	tests := []struct {
		name  string
		input Input
		err   error
	}{
		{
			name:  "request default",
			input: Input{Source: SourceRequest, Mode: "default"},
		},
		{
			name:  "request local",
			input: Input{Source: SourceRequest, Mode: "local"},
		},
		{name: "request label round robin", input: Input{
			Source: SourceRequest, Mode: "label", LabelID: 1,
			Strategy: "round_robin",
		}},
		{
			name:  "schedule inherit",
			input: Input{Source: SourceSchedule, Mode: "inherit"},
		},
		{name: "schedule label all", input: Input{
			Source: SourceSchedule, Mode: "label", LabelID: 1, Strategy: "all",
		}},
		{
			name:  "reject malformed source",
			input: Input{Source: "bogus", Mode: "local"},
			err:   ErrInvalidPolicy,
		},
		{
			name:  "reject request inherit",
			input: Input{Source: SourceRequest, Mode: "inherit"},
			err:   ErrInvalidPolicy,
		},
		{
			name:  "reject schedule default",
			input: Input{Source: SourceSchedule, Mode: "default"},
			err:   ErrInvalidPolicy,
		},
		{
			name:  "reject malformed mode",
			input: Input{Source: SourceRequest, Mode: "remote"},
			err:   ErrInvalidPolicy,
		},
		{
			name: "reject malformed strategy",
			input: Input{
				Source:   SourceRequest,
				Mode:     "label",
				LabelID:  1,
				Strategy: "random",
			},
			err: ErrInvalidPolicy,
		},
		{
			name:  "reject missing label",
			input: Input{Source: SourceRequest, Mode: "label", Strategy: "all"},
			err:   ErrInvalidPolicy,
		},
		{
			name:  "reject local label fields",
			input: Input{Source: SourceRequest, Mode: "local", LabelID: 1},
			err:   ErrInvalidPolicy,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseInput(test.input)
			if !errors.Is(err, test.err) {
				t.Fatalf("ParseInput() error = %v, want %v", err, test.err)
			}
		})
	}
}

func TestRoutingPolicy_ExplicitThenProjectThenLegacyPrecedence(t *testing.T) {
	fixture := newRoutingFixture(t, 1)
	ctx := context.Background()
	_, err := fixture.repo.Queries.CreateProjectExecutionPolicy(
		ctx,
		db.CreateProjectExecutionPolicyParams{
			ProjectID: fixture.project.ID, TargetMode: "label",
			AgentLabelID:  sql.NullInt64{Int64: fixture.label.ID, Valid: true},
			AgentStrategy: sql.NullString{String: "all", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create project policy: %v", err)
	}
	resolver := NewResolver(fixture.repo)
	explicit, err := resolver.Resolve(
		ctx,
		fixture.project.ID,
		fixture.environment.ID,
		Input{
			Source: SourceRequest, Mode: "local",
		},
	)
	if err != nil || explicit.Mode != TargetLocal ||
		explicit.Source != SourceRequest {
		t.Fatalf("explicit policy = %#v, %v", explicit, err)
	}
	project, err := resolver.Resolve(
		ctx,
		fixture.project.ID,
		fixture.environment.ID,
		Input{
			Source: SourceRequest, Mode: "default",
		},
	)
	if err != nil || project.Mode != TargetLabel ||
		project.Strategy != StrategyAll ||
		project.Source != SourceProject {
		t.Fatalf("project policy = %#v, %v", project, err)
	}
	if _, err := fixture.repo.Queries.DeleteProjectExecutionPolicy(
		ctx, fixture.project.ID,
	); err != nil {
		t.Fatalf("delete project policy: %v", err)
	}
	legacy, err := resolver.Resolve(
		ctx,
		fixture.project.ID,
		fixture.environment.ID,
		Input{
			Source: SourceSchedule, Mode: "inherit",
		},
	)
	if err != nil || legacy.Mode != TargetRemote ||
		legacy.LegacyAgentID != "agent-a" || legacy.Source != SourceLegacy {
		t.Fatalf("legacy policy = %#v, %v", legacy, err)
	}
}

func TestRoutingPolicy_NonexistentLabelReturnsTypedError(t *testing.T) {
	fixture := newRoutingFixture(t, 0)
	_, err := NewResolver(fixture.repo).Resolve(
		context.Background(), fixture.project.ID, fixture.environment.ID,
		Input{
			Source: SourceRequest, Mode: "label", LabelID: 999,
			Strategy: "all",
		},
	)
	if !errors.Is(err, ErrLabelNotFound) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrLabelNotFound)
	}
}
