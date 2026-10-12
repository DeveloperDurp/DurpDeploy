package agentserver_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/staging"
	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestWaitingPollCannotBorrowStagingCapability(t *testing.T) {
	testWaitingPollCannotBorrowTransferCapability(t, false, true)
}

func TestWaitingPollCannotBorrowArtifactCapability(t *testing.T) {
	testWaitingPollCannotBorrowTransferCapability(t, true, false)
}

func TestArtifactCapabilityWithdrawalFailsWaitingPackage(t *testing.T) {
	testWaitingPollCannotBorrowTransferCapability(t, true, true)
}

func testWaitingPollCannotBorrowTransferCapability(
	t *testing.T,
	packaged bool,
	withdraw bool,
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
	if withdraw && f.checkTransferWithdrawal(t, id, packaged) {
		return
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

func (f agentFixture) checkTransferWithdrawal(
	t *testing.T,
	id int64,
	packaged bool,
) bool {
	t.Helper()
	// A fresh poll withdraws package support. Staging support stays sticky.
	ctx, stop := context.WithCancel(t.Context())
	fresh := startConcurrentPoll(t, f, concurrentPollCall{
		ctx:     ctx,
		body:    `{"protocol":"agent/1","agent_version":"withdrawn-package"}`,
		staging: packaged,
	})
	waitPollVersion(t, f, "withdrawn-package")
	stop()
	<-fresh
	runs, err := f.repo.Queries.ListRemoteStepRuns(t.Context(),
		db.ListRemoteStepRunsParams{DeploymentID: id, StepIndex: 0})
	if err != nil || len(runs) != 1 {
		t.Fatalf("waiting runs=%v error=%v", runs, err)
	}
	if packaged {
		if runs[0].State != "failed" || !runs[0].FinishedAt.Valid {
			t.Fatalf(
				"withdrawn package capability retained waiting work: %+v",
				runs[0],
			)
		}
		var eligible int
		if err := f.repo.DB.QueryRowContext(
			t.Context(),
			"SELECT COUNT(*) FROM eligible_remote_step_agents WHERE deployment_id=? AND step_index=0",
			id,
		).Scan(&eligible); err != nil || eligible != 0 {
			t.Fatalf(
				"withdrawn package capability remained eligible: %d %v",
				eligible,
				err,
			)
		}
		return true
	}
	if runs[0].State != "waiting" {
		t.Fatalf(
			"fresh unstaged poll invalidated staged queue: %+v",
			runs[0],
		)
	}
	return false
}
