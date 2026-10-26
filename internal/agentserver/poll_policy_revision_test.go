package agentserver_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestWaitingPollCannotBorrowResourcePolicy(t *testing.T) {
	for _, step := range []bool{false, true} {
		for _, withdraw := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("step=%t/withdraw=%t", step, withdraw),
				func(t *testing.T) {
					checkWaitingPollResourcePolicy(t, step, withdraw)
				},
			)
		}
	}
}

func checkWaitingPollResourcePolicy(t *testing.T, step, withdraw bool) {
	t.Helper()
	f := newAgentFixtureWithDSN(t, filepath.Join(t.TempDir(), "policy.db")+
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", nil)
	f.client.Timeout = 27 * time.Second
	advertisement, err := resourcepolicy.Defaults().Advertisement()
	require.NoError(t, err)
	previous, current := "", advertisement
	if withdraw {
		previous, current = current, previous
	}
	old := startConcurrentPoll(t, f, concurrentPollCall{
		ctx: t.Context(), body: `{"protocol":"agent/1","agent_version":"old-policy"}`,
		resourcePolicy: previous,
	})
	waitPollVersion(t, f, "old-policy")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fresh := startConcurrentPoll(t, f, concurrentPollCall{
		ctx: ctx, body: pollBody, resourcePolicy: current,
	})
	waitPollVersion(t, f, "test-v1")
	cancel()
	<-fresh
	id := seedPollPayload(t, f, "pending", "test-agent")
	if step {
		deployment, err := f.repo.Queries.GetDeployment(t.Context(), id)
		require.NoError(t, err)
		_, err = f.repo.DB.ExecContext(t.Context(), `
DELETE FROM remote_deployment_claims WHERE deployment_id=?;
UPDATE deployments SET status='running', assigned_agent_id=NULL WHERE id=?;
INSERT INTO agent_labels(agent_id,label) VALUES('test-agent','policy');
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('test-agent',?);
INSERT INTO deployment_step_selectors(deployment_id,step_index,label) VALUES(?,0,'policy')`,
			id, id, deployment.EnvironmentID, id)
		require.NoError(t, err)
		created, err := f.repo.QueueRemoteStepRuns(t.Context(), id, 0)
		require.NoError(t, err)
		require.EqualValues(t, 1, created)
	}
	select {
	case result := <-old:
		require.NoError(t, result.err)
		require.Equal(t, 204, result.status)
	case <-time.After(5 * time.Second):
		t.Fatal("stale policy poll did not return")
	}
	if step {
		runs, err := f.repo.Queries.ListRemoteStepRuns(
			t.Context(),
			db.ListRemoteStepRunsParams{DeploymentID: id, StepIndex: 0},
		)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		require.Equal(t, "waiting", runs[0].State)
		require.Empty(t, runs[0].ClaimTokenHash)
		require.False(t, runs[0].Ciphertext.Valid)
	} else {
		claim, err := f.repo.Queries.GetRemoteDeploymentClaim(t.Context(), id)
		require.NoError(t, err)
		require.Equal(t, "waiting", claim.State)
		require.Empty(t, claim.ClaimTokenHash)
		require.False(t, claim.Ciphertext.Valid)
	}
	stored, err := f.repo.Queries.GetAgentResourcePolicy(
		t.Context(),
		"test-agent",
	)
	if current == "" {
		require.ErrorIs(t, err, sql.ErrNoRows)
	} else {
		require.NoError(t, err)
		require.Equal(t, current, stored)
	}
	var values []string
	if current != "" {
		values = []string{current}
	}
	response := pollWithResourcePolicy(t, f, values)
	defer response.Body.Close()
	poll := decodePollResponse(t, response)
	require.Equal(t, id, int64(poll.DeploymentID))
	require.NotEmpty(t, poll.ClaimToken)
}
