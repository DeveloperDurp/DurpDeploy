package agentserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	agentpayload "github.com/DeveloperDurp/durpdeploy-agent/payload"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

const containerPollBody = `{"protocol":"agent/3","agent_version":"test-v3",
    "supported_interpreters":[],"execution_modes":["container"],
    "container_runtimes":["podman"],"container_interpreters":["bash"]}`

func TestAgentV3CleanupBlocksQueueUntilReadyPoll(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelFirst), func(t *testing.T) {
			f := newAgentFixture(t)
			id, claim := claimedRemoteStepWithMode(
				t,
				f,
				"container",
				containerPollBody,
			)
			path := func(pattern string) string { return strings.ReplaceAll(pattern, "{id}", fmt.Sprint(id)) }
			assertAgentStatus(
				t,
				postAgent(t, f, path(agentproto.StartPath), claim),
				204,
			)
			if cancelFirst {
				_, err := f.repo.Queries.RequestRemoteStepCancellation(
					t.Context(),
					db.RequestRemoteStepCancellationParams{
						DeploymentID: id,
						Now:          validInt(100),
					},
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			result := strings.TrimSuffix(
				claim,
				"}",
			) + `,"state":"cleanup_unconfirmed","error":"restore runtime"}`
			for range 2 {
				assertAgentStatus(
					t,
					postAgent(t, f, path(agentproto.ResultPath), result),
					204,
				)
			}
			deployment, err := f.repo.Queries.GetDeployment(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.repo.Queries.FailDeploymentsWithTerminalRemoteStepRuns(t.Context(), validInt(101)); err != nil {
				t.Fatal(err)
			}
			next, err := f.repo.Queries.CreateDeployment(
				t.Context(),
				db.CreateDeploymentParams{
					ReleaseID: deployment.ReleaseID, EnvironmentID: deployment.EnvironmentID, Status: "queued",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.repo.ReconcileDeploymentQueues(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertDeploymentStatus(t, f, next.ID, "queued")
			assertAgentStatus(t, postAgent(
				t,
				f,
				path(agentproto.ResultPath),
				strings.Replace(
					result,
					"cleanup_unconfirmed",
					"succeeded",
					1,
				),
			), 409)
			assertAgentStatus(t, postAgent(
				t,
				f,
				path(agentproto.ResultPath),
				strings.Replace(
					result,
					stringClaimToken(claim),
					"wrong-claim",
					1,
				),
			), 409)
			// An agent downgrade cannot assert container reconciliation.
			recordFixturePoll(
				t,
				f,
				agentproto.PollRequest{
					ProtocolEnvelope: agentproto.ProtocolEnvelope{
						Protocol: agentproto.AgentV1,
					},
				},
			)
			assertDeploymentStatus(t, f, next.ID, "queued")
			var ready agentproto.PollRequest
			if err := json.Unmarshal([]byte(containerPollBody), &ready); err != nil {
				t.Fatal(err)
			}
			recordFixturePoll(t, f, ready)
			assertDeploymentStatus(t, f, next.ID, "pending")
			assertDeploymentStatus(t, f, id, "cleanup_unconfirmed")
			runs, err := f.repo.Queries.ListRemoteStepRuns(
				t.Context(),
				db.ListRemoteStepRunsParams{DeploymentID: id, StepIndex: 0},
			)
			if err != nil || len(runs) != 1 ||
				!runs[0].CleanupConfirmedAt.Valid {
				t.Fatalf("runs=%+v error=%v", runs, err)
			}
			assertAgentStatus(
				t,
				postAgent(t, f, path(agentproto.ResultPath), result),
				204,
			)
			assertAgentStatus(
				t,
				postAgent(t, f, path(agentproto.StartPath), claim),
				409,
			)
		})
	}
}

func recordFixturePoll(
	t *testing.T,
	f agentFixture,
	request agentproto.PollRequest,
) {
	t.Helper()
	agent, err := f.repo.Queries.GetAgent(t.Context(), "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.repo.RecordAgentPoll(t.Context(), db.HeartbeatAgentParams{
		ID: agent.ID, CertificateFingerprint: agent.CertificateFingerprint,
		Now: validInt(agent.LastHeartbeatAt.Int64 + 1),
	}, request)
	if err != nil {
		t.Fatal(err)
	}
}

func assertAgentStatus(t *testing.T, response *http.Response, status int) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		t.Fatalf("agent status=%d want=%d", response.StatusCode, status)
	}
}

func assertDeploymentStatus(
	t *testing.T,
	f agentFixture,
	id int64,
	want string,
) {
	t.Helper()
	deployment, err := f.repo.Queries.GetDeployment(t.Context(), id)
	if err != nil || deployment.Status != want {
		t.Fatalf("deployment=%+v want=%s error=%v", deployment, want, err)
	}
}

func stringClaimToken(claim string) string {
	var parsed struct {
		ClaimToken string `json:"claim_token"`
	}
	_ = json.Unmarshal([]byte(claim), &parsed)
	return parsed.ClaimToken
}

func TestAgentV3EncryptedPayloadKeepsStepRestriction(t *testing.T) {
	f := newAgentFixture(t)
	id := seedPollPayload(t, f, "pending", "test-agent")
	if _, err := f.repo.DB.ExecContext(t.Context(), `
        UPDATE deployment_steps SET agent_execution_mode='container', container_image='alpine:3.20',
        variable_names=CASE step_index WHEN 0 THEN '["MODE"]' ELSE '["TOKEN"]' END WHERE deployment_id=?`, id); err != nil {
		t.Fatal(err)
	}
	response := postAgent(t, f, agentproto.PollPath, containerPollBody)
	if response.StatusCode != 200 {
		t.Fatalf("poll status=%d", response.StatusCode)
	}
	poll := decodePollResponse(t, response)
	response.Body.Close()
	plaintext, err := agentpayload.Open(f.identity, id, []byte(poll.Payload))
	if err != nil {
		t.Fatal(err)
	}
	var payload dispatch.Payload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Release.Steps) != 2 || len(payload.Variables) != 2 {
		t.Fatalf("payload=%+v", payload)
	}
	for i, name := range []string{"MODE", "TOKEN"} {
		step := payload.Release.Steps[i]
		if step.ExecutionMode != agentproto.ExecutionContainer ||
			step.ContainerImage != "alpine:3.20" ||
			len(step.VariableNames) != 1 ||
			step.VariableNames[0] != name {
			t.Fatalf("step=%+v", step)
		}
	}
}
