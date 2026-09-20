package agentserver_test

import (
	"net/http"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRemoteLogsRejectLimits(t *testing.T) {
	events := make([]string, agentproto.MaxLogEvents+1)
	for index := range events {
		events[index] = `{"sequence":` + strings.Repeat("0", 1) +
			`,"line":"x"}`
	}
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"event count", `{"protocol":"agent/1","claim_token":"claim-one",` +
			`"events":[` + strings.Join(events, ",") + `]}`,
			http.StatusRequestEntityTooLarge},
		{"line bytes", `{"protocol":"agent/1","claim_token":"claim-one",` +
			`"events":[{"sequence":1,"line":"` +
			strings.Repeat("x", agentproto.MaxLogLineBytes+1) + `"}]}`,
			http.StatusRequestEntityTooLarge},
		{"batch bytes", `{"protocol":"agent/1","claim_token":"claim-one",` +
			`"events":[{"sequence":1,"line":"` +
			strings.Repeat("x", agentproto.MaxLogBatchBytes) + `"}]}`,
			http.StatusRequestEntityTooLarge},
		{"negative sequence", `{"protocol":"agent/1",` +
			`"claim_token":"claim-one","events":[` +
			`{"sequence":-1,"line":"reject"}]}`, http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentFixture(t)
			seedAgentLifecycle(t, fixture.repo)
			startLifecycle(t, fixture, http.StatusNoContent)
			response := postAgent(t, fixture, strings.ReplaceAll(
				agentproto.LogsPath, "{id}", "1",
			), test.body)
			if response.StatusCode != test.status {
				t.Fatalf("status=%d want=%d", response.StatusCode, test.status)
			}
			assertNoDeploymentLogs(t, fixture)
		})
	}
}

func TestRemoteLogsRejectWrongToken(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	startLifecycle(t, fixture, http.StatusNoContent)
	response := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.LogsPath, "{id}", "1",
	), `{"protocol":"agent/1","claim_token":"wrong",`+
		`"events":[{"sequence":1,"line":"reject"}]}`)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d", response.StatusCode)
	}
	assertNoDeploymentLogs(t, fixture)
}

func TestRemoteLogsRejectInvalidUTF8(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	startLifecycle(t, fixture, http.StatusNoContent)
	body := `{"protocol":"agent/1","claim_token":"claim-one",` +
		`"events":[{"sequence":1,"line":"` + string([]byte{0xff}) + `"}]}`
	response := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.LogsPath, "{id}", "1",
	), body)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", response.StatusCode)
	}
	assertNoDeploymentLogs(t, fixture)
}

func TestRemoteTerminalRejectsConflict(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	startLifecycle(t, fixture, http.StatusNoContent)
	path := strings.ReplaceAll(agentproto.ResultPath, "{id}", "1")
	failed := `{"protocol":"agent/1","claim_token":"claim-one",` +
		`"state":"failed","error":"first"}`
	if response := postAgent(
		t,
		fixture,
		path,
		failed,
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf("first result status=%d", response.StatusCode)
	}
	succeeded := `{"protocol":"agent/1","claim_token":"claim-one",` +
		`"state":"succeeded","error":""}`
	if response := postAgent(
		t,
		fixture,
		path,
		succeeded,
	); response.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting result status=%d", response.StatusCode)
	}
	assertNotificationCount(t, fixture, 1)
}

func assertNoDeploymentLogs(t *testing.T, fixture agentFixture) {
	t.Helper()
	var count int
	if err := fixture.repo.DB.QueryRow(
		"SELECT COUNT(*) FROM deployment_logs WHERE deployment_id=1",
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deployment logs=%d error=%v", count, err)
	}
}
