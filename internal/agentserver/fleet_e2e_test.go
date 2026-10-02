package agentserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentFleetDrainE2E(t *testing.T) {
	// Given: real admin/API and mTLS agent servers with issued and queued work.
	f := newAgentFixture(t)
	f.client.Timeout = 27 * time.Second
	srv := fleetAdminServer(t, f)
	first := seedPollPayload(t, f, "pending", "test-agent")
	poll := decodePollResponse(
		t,
		postAgent(t, f, agentproto.PollPath, pollBody),
	)
	second := seedPollPayload(t, f, "pending", "test-agent")

	// When: an administrator drains the agent through the public API.
	fleetRequest(
		t,
		srv,
		"POST",
		"/api/v1/admin/agents/test-agent/drain",
		"admin",
		"",
		204,
	)
	fleetRequest(
		t,
		srv,
		"POST",
		"/api/v1/admin/agents/test-agent/drain",
		"admin",
		"",
		204,
	)

	// Then: the real agent can report health but cannot claim its queued deployment.
	blocked := postAgent(t, f, agentproto.PollPath, pollBody)
	if blocked.StatusCode != http.StatusNoContent {
		t.Fatalf("drained poll status=%d", blocked.StatusCode)
	}
	state := fleetAgentState(t, srv)
	if !state.Agent.Draining || state.Agent.Queued != 1 ||
		len(state.Agent.Current) != 1 || state.Agent.Current[0].ID != first ||
		state.Agent.Health != "healthy" {
		t.Fatalf("drained state=%+v", state)
	}
	claim := fmt.Sprintf(
		`{"protocol":"agent/1","claim_token":%q}`,
		poll.ClaimToken,
	)
	for _, path := range []string{agentproto.StartPath, agentproto.HeartbeatPath} {
		response := postAgent(
			t,
			f,
			strings.ReplaceAll(path, "{id}", fmt.Sprint(first)),
			claim,
		)
		want := 204
		if path == agentproto.HeartbeatPath {
			want = 200
		}
		if response.StatusCode != want {
			t.Fatalf("issued work %s status=%d", path, response.StatusCode)
		}
	}
	result := fmt.Sprintf(
		`{"protocol":"agent/1","claim_token":%q,"state":"succeeded","error":""}`,
		poll.ClaimToken,
	)
	response := postAgent(
		t,
		f,
		strings.ReplaceAll(agentproto.ResultPath, "{id}", fmt.Sprint(first)),
		result,
	)
	if response.StatusCode != 204 {
		t.Fatalf("result status=%d", response.StatusCode)
	}
	state = fleetAgentState(t, srv)
	if state.Agent.Success == nil || state.Agent.Success.ID != first ||
		len(state.Agent.Current) != 0 {
		t.Fatalf("completed state=%+v", state)
	}
	fleetRequest(
		t,
		srv,
		"POST",
		"/api/v1/admin/agents/test-agent/resume",
		"admin",
		"",
		204,
	)
	resumed := decodePollResponse(
		t,
		postAgent(t, f, agentproto.PollPath, pollBody),
	)
	if int64(resumed.DeploymentID) != second {
		t.Fatalf("resumed deployment=%d want=%d", resumed.DeploymentID, second)
	}
	var audits struct {
		Items []struct {
			Action string `json:"action"`
		} `json:"items"`
	}
	if err := json.Unmarshal(
		fleetRequest(t, srv, "GET", "/api/v1/admin/audit", "admin", "", 200),
		&audits,
	); err != nil {
		t.Fatal(err)
	}
	drains, resumes := 0, 0
	for _, entry := range audits.Items {
		switch entry.Action {
		case "drain_agent":
			drains++
		case "resume_agent":
			resumes++
		}
	}
	if drains != 1 || resumes != 1 {
		t.Fatalf("audit drains=%d resumes=%d", drains, resumes)
	}
}

func TestAgentFleetHealthAlertsE2E(t *testing.T) {
	// Given: a live agent and the real notification-history API.
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	for _, scenario := range []struct {
		age  int64
		want string
	}{
		{0, ""}, {120, "agent_stale"}, {600, "agent_offline"}, {0, "agent_recovered"},
	} {
		if _, err := f.repo.DB.Exec(`UPDATE agents
			SET last_heartbeat_at=unixepoch()-? WHERE id='test-agent'`, scenario.age); err != nil {
			t.Fatal(err)
		}
		// When: repeated maintenance ticks observe each heartbeat transition.
		for range 2 {
			if err := f.agents.Maintain(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Then: the public API contains exactly one alert for each transition.
	var notifications []struct {
		Type string `json:"event_type"`
	}
	if err := json.Unmarshal(
		fleetRequest(
			t,
			srv,
			"GET",
			"/api/v1/admin/notifications",
			"admin",
			"",
			200,
		),
		&notifications,
	); err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, event := range notifications {
		counts[event.Type]++
	}
	if len(notifications) != 3 || counts["agent_stale"] != 1 ||
		counts["agent_offline"] != 1 ||
		counts["agent_recovered"] != 1 {
		t.Fatalf("health events=%+v", notifications)
	}
}

func TestAgentFleetAdminBoundaryE2E(t *testing.T) {
	// Given: browser sessions and bearer tokens for all roles.
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	for _, role := range []string{"viewer", "deployer"} {
		for _, path := range []string{"/api/v1/admin/agents", "/api/v1/admin/agents/test-agent"} {
			// When: a non-admin reads agent administration.
			fleetRequest(t, srv, "GET", path, role, "", 403)
		}
		for _, action := range []string{"drain", "resume"} {
			// Then: both API writes and web forms reject non-admin users.
			fleetRequest(
				t,
				srv,
				"POST",
				"/api/v1/admin/agents/test-agent/"+action,
				role,
				"",
				403,
			)
		}
	}
	fleetRequest(
		t,
		srv,
		"POST",
		"/api/v1/admin/agents/missing/drain",
		"admin",
		"",
		404,
	)
	if _, err := f.repo.RevokeAgent(t.Context(), "test-agent"); err != nil {
		t.Fatal(err)
	}
	fleetRequest(
		t,
		srv,
		"POST",
		"/api/v1/admin/agents/test-agent/resume",
		"admin",
		"",
		409,
	)
}
