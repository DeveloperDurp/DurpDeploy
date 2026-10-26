package agentserver_test

import (
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestPollCustomPolicyClaims(t *testing.T) {
	for _, step := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "deployment", true: "step"}[step],
			func(t *testing.T) {
				settings := config.Defaults()
				settings.Limits.Execution.MaxRetries = 11
				settings.Limits.Execution.MaxTimeoutSeconds = 90000
				settings.Limits.Container.PIDs = 96
				settings.Limits.Staging.PayloadBytes = 4096
				settings.Limits.Artifact.DownloadBytes++
				f := newAgentFixtureWithDSN(
					t,
					":memory:?_pragma=foreign_keys(1)",
					nil,
					settings,
				)
				id := seedPollPayload(t, f, "pending", "test-agent")
				_, err := f.repo.DB.ExecContext(
					t.Context(),
					"UPDATE deployment_steps SET max_retries=11,timeout_seconds=90000 WHERE deployment_id=?",
					id,
				)
				require.NoError(t, err)
				advertisement, err := settings.Limits.AgentPolicy().
					Advertisement()
				require.NoError(t, err)
				if step {
					deployment, err := f.repo.Queries.GetDeployment(
						t.Context(),
						id,
					)
					require.NoError(t, err)
					_, err = f.repo.DB.ExecContext(t.Context(), `
DELETE FROM remote_deployment_claims WHERE deployment_id=?;
UPDATE deployments SET status='running',assigned_agent_id=NULL WHERE id=?;
INSERT INTO agent_labels(agent_id,label) VALUES('test-agent','custom');
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('test-agent',?);
INSERT INTO deployment_step_selectors(deployment_id,step_index,label) VALUES(?,0,'custom');`,
						id, id, deployment.EnvironmentID, id)
					require.NoError(t, err)
					// Admission requires the current policy before work is queued.
					require.NoError(
						t,
						f.repo.Queries.AddAgentResourcePolicy(
							t.Context(),
							db.AddAgentResourcePolicyParams{
								AgentID: "test-agent", Advertisement: advertisement,
							},
						),
					)
					n, err := f.repo.QueueRemoteStepRuns(t.Context(), id, 0)
					require.NoError(t, err)
					require.EqualValues(t, 1, n)
				}
				response := pollWithResourcePolicy(
					t,
					f,
					[]string{advertisement},
				)
				defer response.Body.Close()
				poll := decodePollResponse(t, response)
				require.Equal(t, id, int64(poll.DeploymentID))
				require.NotEmpty(t, poll.ClaimToken)
				require.NotEmpty(t, poll.Payload)
			},
		)
	}
}
