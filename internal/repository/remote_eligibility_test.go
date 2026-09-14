package repository_test

import (
	"context"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentClaimRequiresEligibility(t *testing.T) {
	for _, scenario := range []struct {
		name string
		sql  string
	}{
		{"no selectors", "DELETE FROM deployment_step_selectors WHERE deployment_id = 1"},
		{"no matching label", "DELETE FROM agent_labels WHERE agent_id = 'a'"},
		{"no assignment", "DELETE FROM environment_agent_assignments WHERE agent_id = 'a'"},
		{"disabled", "UPDATE agents SET status = 'disabled' WHERE id = 'a'"},
		{"revoked", "UPDATE agents SET status = 'revoked', revoked_at = 100 WHERE id = 'a'"},
		{"stale heartbeat", "UPDATE agents SET last_heartbeat_at = 89 WHERE id = 'a'"},
		{"unpaired", "DELETE FROM agent_pairings WHERE agent_id = 'a'"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r := remoteFixture(t)
			if _, err := r.DB.Exec(scenario.sql); err != nil {
				t.Fatal(err)
			}
			n, err := r.ClaimRemoteStep(context.Background(), claimArg("a"))
			assertZero(t, n, err)
			row, err := r.Queries.GetDeploymentStepAttempt(
				context.Background(),
				db.GetDeploymentStepAttemptParams{
					DeploymentID: 1,
					StepIndex:    0,
					Attempt:      1,
				},
			)
			if err != nil || row.State != "waiting" || row.AgentID.Valid {
				t.Fatalf(
					"state=%s occupied=%v error=%v",
					row.State,
					row.AgentID.Valid,
					err,
				)
			}
			t.Log("rejected rows=0; persisted state=waiting; agent=NULL")
		})
	}
}
