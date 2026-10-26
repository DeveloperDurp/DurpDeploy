package repository_test

import (
	"testing"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestRemoteResourcePolicyFanoutAdmissionIsAtomic(t *testing.T) {
	r := remoteFixture(t)
	seedPolicyStep(t, r, false)
	_, err := r.DB.ExecContext(t.Context(), `
INSERT INTO agent_labels VALUES('b','other');
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('b',2);`)
	require.NoError(t, err)
	settings := config.Defaults()
	settings.Limits.Execution.MaxRetries++
	configured := repository.NewConfigured(r.DB, settings)
	advertisement, err := settings.Limits.AgentPolicy().Advertisement()
	require.NoError(t, err)
	defaults, err := config.Defaults().Limits.AgentPolicy().Advertisement()
	require.NoError(t, err)
	require.NoError(
		t,
		r.Queries.AddAgentResourcePolicy(
			t.Context(),
			db.AddAgentResourcePolicyParams{
				AgentID: "a", Advertisement: advertisement,
			},
		),
	)
	for _, second := range []string{"", defaults} {
		if second != "" {
			require.NoError(
				t,
				r.Queries.AddAgentResourcePolicy(
					t.Context(),
					db.AddAgentResourcePolicyParams{
						AgentID: "b", Advertisement: second,
					},
				),
			)
		}
		n, err := configured.QueueRemoteStepRuns(t.Context(), 3, 0)
		require.Zero(t, n)
		require.ErrorIs(t, err, agentexecution.ErrLimits)
		runs, err := r.Queries.ListRemoteStepRuns(
			t.Context(),
			db.ListRemoteStepRunsParams{
				DeploymentID: 3, StepIndex: 0,
			},
		)
		require.NoError(t, err)
		require.Empty(t, runs)
	}
	require.NoError(t, r.Queries.ClearAgentResourcePolicy(t.Context(), "b"))
	require.NoError(
		t,
		r.Queries.AddAgentResourcePolicy(
			t.Context(),
			db.AddAgentResourcePolicyParams{
				AgentID: "b", Advertisement: advertisement,
			},
		),
	)
	n, err := configured.QueueRemoteStepRuns(t.Context(), 3, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	// A queued target can lose its policy before claim preparation.
	require.NoError(t, r.Queries.ClearAgentResourcePolicy(t.Context(), "a"))
	_, claimed, err := configured.ClaimRemoteStepPayload(
		t.Context(),
		"a",
		func(repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
			t.Fatal("withdrawn policy reached payload preparation")
			return repository.RemotePreparedClaim{}, nil
		},
	)
	require.NoError(t, err)
	require.False(t, claimed)
	var ciphertext int
	require.NoError(t, r.DB.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM remote_step_runs WHERE ciphertext IS NOT NULL",
	).Scan(&ciphertext))
	require.Zero(t, ciphertext)
}
