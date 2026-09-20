package repository_test

import (
	"context"
	"testing"
)

func TestAgentClaimRequiresEligibility(t *testing.T) {
	for _, scenario := range []struct {
		name string
		sql  string
	}{
		{"wrong snapshot", "UPDATE deployments SET assigned_agent_id = 'b' WHERE id = 1"},
		{"disabled", "UPDATE agents SET status = 'disabled' WHERE id = 'a'"},
		{"revoked", "UPDATE agents SET status = 'revoked', revoked_at = 100 WHERE id = 'a'"},
		{"unpaired", "DELETE FROM agent_pairings WHERE agent_id = 'a'"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r := remoteFixture(t)
			if _, err := r.DB.Exec(scenario.sql); err != nil {
				t.Fatal(err)
			}
			n, err := r.ClaimRemoteDeployment(
				context.Background(),
				claimArg("a"),
			)
			assertZero(t, n, err)
			row, err := r.Queries.GetRemoteDeploymentClaim(
				context.Background(),
				1,
			)
			if err != nil || row.State != "waiting" || row.AgentID != "a" {
				t.Fatalf(
					"state=%s agent=%s error=%v",
					row.State,
					row.AgentID,
					err,
				)
			}
			t.Log("rejected rows=0; persisted state=waiting")
		})
	}
}
