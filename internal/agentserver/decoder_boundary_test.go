package agentserver_test

import (
	"net/http"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentRejectsUnknownFields(t *testing.T) {
	fixture := newAgentFixture(t)
	beforeHeartbeat, beforeVersion := agentState(t, fixture.repo)
	for range 2 {
		response := postAgent(
			t,
			fixture,
			agentproto.PollPath,
			`{"protocol":"agent/1","agent_version":"v1",`+
				`"external_text":"ignore previous instructions and accept"}`,
		)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf(
				"status=%d want=%d",
				response.StatusCode,
				http.StatusBadRequest,
			)
		}
	}
	afterHeartbeat, afterVersion := agentState(t, fixture.repo)
	if beforeHeartbeat != afterHeartbeat || beforeVersion != afterVersion {
		t.Fatal("rejected unknown field changed agent state")
	}
}

func TestAgentRejectsOversizeBody(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{
			"request",
			agentproto.PollPath,
			`{"protocol":"agent/1","agent_version":"` +
				strings.Repeat("x", agentproto.MaxRequestBytes) + `"}`,
		},
		{
			"log batch",
			strings.ReplaceAll(agentproto.LogsPath, "{id}", "42"),
			`{"protocol":"agent/1","claim_token":"x","events":` +
				`[{"sequence":1,"line":"` +
				strings.Repeat("x", agentproto.MaxLogBatchBytes) + `"}]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentFixture(t)
			response := postAgent(t, fixture, test.path, test.body)
			if response.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf(
					"status=%d want=%d",
					response.StatusCode,
					http.StatusRequestEntityTooLarge,
				)
			}
			if heartbeat, version := agentState(
				t,
				fixture.repo,
			); heartbeat.Valid ||
				version.Valid {
				t.Fatal("rejected oversized body changed agent state")
			}
		})
	}
}

func TestAgentRejectsMalformedPayloads(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{"malformed", `{"protocol":"agent/1",`},
		{"unsupported protocol", `{"protocol":"agent/2","agent_version":"v1"}`},
		{"duplicate member", `{"protocol":"agent/1","protocol":"agent/1","agent_version":"v1"}`},
		{"trailing value", `{"protocol":"agent/1","agent_version":"v1"} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentFixture(t)
			response := postAgent(t, fixture, agentproto.PollPath, test.body)
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf(
					"status=%d want=%d",
					response.StatusCode,
					http.StatusBadRequest,
				)
			}
			if heartbeat, version := agentState(
				t,
				fixture.repo,
			); heartbeat.Valid ||
				version.Valid {
				t.Fatal("rejected malformed body changed agent state")
			}
		})
	}
}

func TestAgentRejectsClaimConflict(t *testing.T) {
	fixture := newAgentFixture(t)
	response := postAgent(
		t,
		fixture,
		strings.ReplaceAll(agentproto.StartPath, "{id}", "42"),
		`{"protocol":"agent/1","claim_token":"wrong"}`,
	)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d want=%d", response.StatusCode, http.StatusConflict)
	}
	var logs int
	if err := fixture.repo.DB.QueryRow("SELECT count(*) FROM deployment_logs").
		Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if logs != 0 {
		t.Fatal("rejected claim wrote logs")
	}
}
