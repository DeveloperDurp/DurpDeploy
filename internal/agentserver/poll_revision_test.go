package agentserver_test

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/staging"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestWaitingLegacyPollCannotClaimNewV3Capabilities(t *testing.T) {
	for _, protocol := range []string{"agent/1", "agent/2", "agent/3"} {
		t.Run(protocol, func(t *testing.T) {
			// Given an authenticated legacy poll waiting for work.
			f := newAgentFixtureWithDSN(
				t,
				filepath.Join(t.TempDir(), "poll.db")+
					"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
				nil,
			)
			capabilities := ""
			if protocol != "agent/1" {
				capabilities = `,"supported_interpreters":["bash"]`
			}
			if protocol == "agent/3" {
				capabilities += `,"execution_modes":["host"],"container_runtimes":[],"container_interpreters":[]`
			}
			legacy := fmt.Sprintf(
				`{"protocol":%q,"agent_version":"waiting-legacy"%s}`,
				protocol,
				capabilities,
			)
			old := startConcurrentPoll(
				t,
				f,
				concurrentPollCall{ctx: t.Context(), body: legacy},
			)
			waitPollVersion(t, f, "waiting-legacy")

			// When a concurrent HTTP poll replaces that identity's capabilities.
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			newPoll := startConcurrentPoll(
				t,
				f,
				concurrentPollCall{ctx: ctx, body: containerPollBody},
			)
			waitPollVersion(t, f, "test-v3")
			cancel()
			<-newPoll
			id := seedPollPayload(t, f, "pending", "test-agent")
			if _, err := f.repo.DB.ExecContext(
				t.Context(),
				`UPDATE deployment_steps
				SET agent_execution_mode='container', container_image='alpine:3.20',
				variable_names='[]' WHERE deployment_id=?`,
				id,
			); err != nil {
				t.Fatal(err)
			}

			// Then the stale request gets no work and does not issue a claim.
			select {
			case result := <-old:
				if result.err != nil || result.status != http.StatusNoContent {
					t.Fatalf(
						"legacy poll status=%d error=%v",
						result.status,
						result.err,
					)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("stale legacy poll did not return")
			}
			claim, err := f.repo.Queries.GetRemoteDeploymentClaim(
				t.Context(),
				id,
			)
			if err != nil || claim.State != "waiting" ||
				claim.ClaimTokenHash != nil {
				t.Fatalf("stale poll issued claim=%+v error=%v", claim, err)
			}
			assertAgentStatus(
				t,
				postAgent(t, f, agentproto.PollPath, containerPollBody),
				200,
			)
		})
	}
}

type concurrentPollResult struct {
	status int
	err    error
}

type concurrentPollCall struct {
	ctx     context.Context
	body    string
	staging bool
}

func startConcurrentPoll(
	t *testing.T,
	f agentFixture,
	call concurrentPollCall,
) <-chan concurrentPollResult {
	t.Helper()
	request, err := http.NewRequestWithContext(call.ctx, http.MethodPost,
		f.server.URL+agentproto.PollPath, strings.NewReader(call.body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if call.staging {
		request.Header.Set(staging.CapabilityHeader, staging.Capability)
	}
	done := make(chan concurrentPollResult, 1)
	go func() {
		response, err := f.client.Do(request)
		result := concurrentPollResult{err: err}
		if response != nil {
			result.status = response.StatusCode
			response.Body.Close()
		}
		done <- result
	}()
	return done
}

func TestWaitingPollCannotBorrowStagingCapability(t *testing.T) {
	f := newAgentFixtureWithDSN(
		t,
		filepath.Join(t.TempDir(), "staging-poll.db")+
			"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		nil,
	)
	old := startConcurrentPoll(
		t,
		f,
		concurrentPollCall{ctx: t.Context(), body: pollBody},
	)
	waitPollVersion(t, f, "test-v1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	newPoll := startConcurrentPoll(t, f, concurrentPollCall{
		ctx: ctx, staging: true,
		body: `{"protocol":"agent/1","agent_version":"waiting-staging"}`,
	})
	waitPollVersion(t, f, "waiting-staging")
	cancel()
	<-newPoll
	id := seedPollPayload(t, f, "pending", "test-agent")
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
		ctx:  staleCtx,
		body: `{"protocol":"agent/1","agent_version":"stale-staging"}`,
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
	poll := decodePollResponse(
		t,
		postAgentStaging(t, f, agentproto.PollPath, pollBody),
	)
	if int64(poll.DeploymentID) != id {
		t.Fatalf("staged poll deployment=%d want=%d", poll.DeploymentID, id)
	}
}

func waitPollVersion(t *testing.T, f agentFixture, version string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		agent, err := f.repo.Queries.GetAgent(t.Context(), "test-agent")
		if err != nil {
			t.Fatal(err)
		}
		if agent.AgentVersion.String == version {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("poll version %s was not recorded", version)
		}
	}
}
