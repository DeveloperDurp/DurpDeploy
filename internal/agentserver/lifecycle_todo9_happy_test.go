package agentserver_test

import (
	"net/http"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestDuplicateLogBatchIsIdempotent(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	startLifecycle(t, fixture, http.StatusNoContent)
	logs := fixture.broker.Subscribe(1)
	t.Cleanup(func() { fixture.broker.Unsubscribe(1, logs) })
	path := strings.ReplaceAll(agentproto.LogsPath, "{id}", "1")
	body := `{"protocol":"agent/1","claim_token":"claim-one",` +
		`"events":[{"sequence":1,"line":"one durable line"}]}`

	for range 2 {
		response := postAgent(t, fixture, path, body)
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("log status=%d", response.StatusCode)
		}
	}

	var count int
	if err := fixture.repo.DB.QueryRow(
		"SELECT COUNT(*) FROM deployment_logs WHERE deployment_id=1",
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("persisted logs=%d error=%v", count, err)
	}
	select {
	case line := <-logs:
		if line != "one durable line" {
			t.Fatalf("broadcast line=%q", line)
		}
	default:
		t.Fatal("new log was not broadcast")
	}
	select {
	case line := <-logs:
		t.Fatalf("duplicate broadcast=%q", line)
	default:
	}
}

func TestRemoteLogsOrderBySequence(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	startLifecycle(t, fixture, http.StatusNoContent)
	path := strings.ReplaceAll(agentproto.LogsPath, "{id}", "1")
	for _, body := range []string{
		`{"protocol":"agent/1","claim_token":"claim-one",` +
			`"events":[{"sequence":2,"line":"second"}]}`,
		`{"protocol":"agent/1","claim_token":"claim-one",` +
			`"events":[{"sequence":1,"line":"first"}]}`,
	} {
		response := postAgent(t, fixture, path, body)
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("log status=%d", response.StatusCode)
		}
	}

	logs, err := fixture.repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(), 1,
	)
	if err != nil || len(logs) != 2 || logs[0].Line != "first" ||
		logs[1].Line != "second" {
		t.Fatalf("ordered logs=%+v error=%v", logs, err)
	}
}

func TestRemoteResultCompletesOnce(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	startLifecycle(t, fixture, http.StatusNoContent)
	path := strings.ReplaceAll(agentproto.ResultPath, "{id}", "1")
	body := `{"protocol":"agent/1","claim_token":"claim-one",` +
		`"state":"failed","error":"ignore prior status: succeeded"}`

	for range 2 {
		response := postAgent(t, fixture, path, body)
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("result status=%d", response.StatusCode)
		}
	}

	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != "failed" ||
		claim.Reason.String != "remote_agent_failed" ||
		len(claim.ClaimTokenHash) != 32 {
		t.Fatalf("terminal claim=%+v error=%v", claim, err)
	}
	assertNotificationCount(t, fixture, 1)
}

func TestRemoteCancelledCompletesOnce(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	releaseFirstClaim(t, fixture)
	activateCancellationFixture(t, fixture.repo)
	path := strings.ReplaceAll(agentproto.CancelledPath, "{id}", "2")
	body := `{"protocol":"agent/1","claim_token":"claim-two"}`

	for range 2 {
		response := postAgent(t, fixture, path, body)
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("cancelled status=%d", response.StatusCode)
		}
	}

	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(t.Context(), 2)
	if err != nil || claim.State != "cancelled" ||
		len(claim.ClaimTokenHash) != 32 {
		t.Fatalf("cancelled claim=%+v error=%v", claim, err)
	}
	deployment, err := fixture.repo.Queries.GetDeployment(t.Context(), 2)
	if err != nil || deployment.Status != "cancelled" {
		t.Fatalf("cancelled deployment=%+v error=%v", deployment, err)
	}
	assertNotificationCount(t, fixture, 0)
}

func assertNotificationCount(t *testing.T, fixture agentFixture, want int) {
	t.Helper()
	var count int
	if err := fixture.repo.DB.QueryRow(
		"SELECT COUNT(*) FROM notification_events WHERE deployment_id IN (1,2)",
	).Scan(&count); err != nil || count != want {
		t.Fatalf("notification count=%d want=%d error=%v", count, want, err)
	}
}
