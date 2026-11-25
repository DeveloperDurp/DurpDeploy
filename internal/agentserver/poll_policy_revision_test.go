package agentserver_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestWaitingPollSurvivesResourcePolicyChange(t *testing.T) {
	f := newAgentFixtureWithDSN(t, filepath.Join(t.TempDir(), "policy.db")+
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", nil)
	f.client.Timeout = 27 * time.Second
	advertisement, err := resourcepolicy.Defaults().Advertisement()
	require.NoError(t, err)
	limits := resourcepolicy.Defaults()
	limits.Container.TmpfsBytes = 4 << 20
	changed, err := limits.Advertisement()
	require.NoError(t, err)
	old := startConcurrentPoll(t, f, concurrentPollCall{
		ctx: t.Context(), body: `{"protocol":"agent/1","agent_version":"old-policy"}`,
		resourcePolicy: advertisement,
	})
	waitPollVersion(t, f, "old-policy")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fresh := startConcurrentPoll(t, f, concurrentPollCall{
		ctx: ctx, body: pollBody, resourcePolicy: changed,
	})
	waitPollVersion(t, f, "test-v1")
	cancel()
	<-fresh
	id := seedPollPayload(t, f, "pending", "test-agent")
	select {
	case result := <-old:
		require.NoError(t, result.err)
		require.Equal(t, http.StatusOK, result.status)
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not return a claim after the policy change")
	}
	claim, err := f.repo.Queries.GetRemoteDeploymentClaim(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "claimed", claim.State)
	require.NotEmpty(t, claim.ClaimTokenHash)
}
