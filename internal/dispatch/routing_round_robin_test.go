package dispatch

import (
	"context"
	"testing"
)

func TestRoundRobin_SelectsFirstNextAndWraps(t *testing.T) {
	fixture := newRoutingFixture(t, 3)
	resolver := NewResolver(fixture.repo)
	policy := Policy{
		Source: SourceRequest, Mode: TargetLabel, LabelID: fixture.label.ID,
		LabelName: fixture.label.Name, Strategy: StrategyRoundRobin,
	}
	want := []string{
		"agent-a", "agent-b", "agent-c", "agent-a", "agent-b", "agent-c",
	}
	for turn, wantID := range want {
		deployment := fixture.createDeployment(t)
		if err := resolver.Freeze(
			context.Background(), deployment.ID, policy,
		); err != nil {
			t.Fatalf("freeze turn %d: %v", turn, err)
		}
		agents, err := resolver.SelectedAgents(
			context.Background(),
			deployment.ID,
		)
		if err != nil || len(agents) != 1 || agents[0].ID != wantID {
			t.Fatalf(
				"turn %d agents = %#v, %v; want %s",
				turn,
				agents,
				err,
				wantID,
			)
		}
		cursor, err := fixture.repo.Queries.GetAgentLabelCursor(
			context.Background(), fixture.label.ID,
		)
		if err != nil || cursor.LastAgentID.String != wantID {
			t.Fatalf(
				"turn %d cursor = %#v, %v; want %s",
				turn,
				cursor,
				err,
				wantID,
			)
		}
		t.Logf(
			"turn=%d deployment=%d selected=%s cursor=%s",
			turn+1, deployment.ID, agents[0].ID, cursor.LastAgentID.String,
		)
	}
}
