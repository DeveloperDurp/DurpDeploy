package agentserver_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/staging"
	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestWaitingPollCannotBorrowStagingCapability(t *testing.T) {
	testWaitingPollCannotBorrowTransferCapability(t, false)
}

func TestWaitingPollCannotBorrowArtifactCapability(t *testing.T) {
	testWaitingPollCannotBorrowTransferCapability(t, true)
}

func testWaitingPollCannotBorrowTransferCapability(
	t *testing.T,
	packaged bool,
) {
	f := newAgentFixtureWithDSN(
		t,
		filepath.Join(t.TempDir(), "staging-poll.db")+
			"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		nil,
	)
	old := startConcurrentPoll(
		t,
		f,
		concurrentPollCall{ctx: t.Context(), body: pollBody, staging: packaged},
	)
	waitPollVersion(t, f, "test-v1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	newPoll := startConcurrentPoll(t, f, concurrentPollCall{
		ctx: ctx, staging: true, artifact: packaged,
		body: `{"protocol":"agent/1","agent_version":"waiting-staging"}`,
	})
	waitPollVersion(t, f, "waiting-staging")
	cancel()
	<-newPoll
	id := seedPollPayload(t, f, "pending", "test-agent")
	if packaged {
		seedAgentArtifact(t, f, id, nil)
	}
	deployment, err := f.repo.Queries.GetDeployment(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.ExecContext(t.Context(), `
		DELETE FROM remote_deployment_claims WHERE deployment_id=?;
		UPDATE deployments SET status='running', assigned_agent_id=NULL WHERE id=?;
		INSERT INTO agent_labels(agent_id,label) VALUES('test-agent','staging');
		INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('test-agent',?);
		INSERT INTO deployment_step_selectors(deployment_id,step_index,label) VALUES(?,0,'staging')`,
		id, id, deployment.EnvironmentID, id); err != nil {
		t.Fatal(err)
	}
	seedStageInput(t, f, id)
	if created, err := f.repo.QueueRemoteStepRuns(
		t.Context(),
		id,
		0,
	); err != nil ||
		created != 1 {
		t.Fatalf("queue rows=%d error=%v", created, err)
	}
	select {
	case result := <-old:
		if result.err != nil || result.status != http.StatusNoContent {
			t.Fatalf(
				"unstaged poll status=%d error=%v",
				result.status,
				result.err,
			)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stale unstaged poll did not return")
	}
	// A later unstaged request must not destroy the queued staged claim.
	staleCtx, stopStale := context.WithCancel(t.Context())
	stale := startConcurrentPoll(t, f, concurrentPollCall{
		ctx:     staleCtx,
		body:    `{"protocol":"agent/1","agent_version":"stale-staging"}`,
		staging: packaged,
	})
	waitPollVersion(t, f, "stale-staging")
	stopStale()
	<-stale
	var waiting int
	if err := f.repo.DB.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM remote_step_runs WHERE deployment_id=? AND state='waiting'",
		id,
	).Scan(&waiting); err != nil || waiting != 1 {
		t.Fatalf("stale poll invalidated staged queue: %d %v", waiting, err)
	}
	request, err := http.NewRequestWithContext(t.Context(), "POST",
		f.server.URL+agentproto.PollPath, strings.NewReader(pollBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(staging.CapabilityHeader, staging.Capability)
	if packaged {
		request.Header.Set(
			agentartifact.CapabilityHeader,
			agentartifact.Capability,
		)
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	poll := decodePollResponse(t, response)
	if int64(poll.DeploymentID) != id {
		t.Fatalf("staged poll deployment=%d want=%d", poll.DeploymentID, id)
	}
}
