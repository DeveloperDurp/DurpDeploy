package agentserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/events"

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
	deadline := time.Now().Add(2 * time.Second)
	for {
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
		if len(notifications) == 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
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

type blockedFleetNotifier struct {
	entered chan events.Type
	release chan struct{}
}

func (n blockedFleetNotifier) Name() string { return "blocked" }

func (n blockedFleetNotifier) Notify(
	_ context.Context,
	evt events.Event,
) (bool, error) {
	n.entered <- evt.Type
	<-n.release
	return false, nil
}

func TestAgentFleetSlowNotifierDoesNotBlockMaintenanceE2E(t *testing.T) {
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	n := blockedFleetNotifier{make(chan events.Type, 2), make(chan struct{})}
	f.bus.Register(n)
	t.Cleanup(func() {
		select {
		case <-n.release:
		default:
			close(n.release)
		}
	})
	if _, err := f.repo.DB.Exec(`UPDATE agents SET
		last_heartbeat_at=unixepoch()-120 WHERE id='test-agent'`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.agents.Maintain(t.Context()) }()
	select {
	case typ := <-n.entered:
		if typ != events.AgentStale {
			t.Fatalf("first alert=%s", typ)
		}
	case <-time.After(time.Second):
		t.Fatal("health alert was not dispatched")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("notifier blocked maintenance")
	}
	deployment := seedPollPayload(t, f, "pending", "test-agent")
	decodePollResponse(t, postAgent(t, f, agentproto.PollPath, pollBody))
	if _, err := f.repo.DB.Exec(`UPDATE remote_deployment_claims
		SET claim_expires_at=0 WHERE deployment_id=?`, deployment); err != nil {
		t.Fatal(err)
	}
	if err := f.agents.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	state := fleetAgentState(t, srv)
	if len(state.Agent.Current) != 0 || state.Agent.Queued != 1 {
		t.Fatalf("expired claim did not return to queue: %+v", state)
	}
	select {
	case typ := <-n.entered:
		t.Fatalf("alert %s overtook blocked stale delivery", typ)
	case <-time.After(100 * time.Millisecond):
	}
	close(n.release)
	select {
	case typ := <-n.entered:
		if typ != events.AgentRecovered {
			t.Fatalf("second alert=%s", typ)
		}
	case <-time.After(time.Second):
		t.Fatal("queued recovery alert was not delivered")
	}
	var notifications []struct {
		Type string `json:"event_type"`
	}
	deadline := time.Now().Add(time.Second)
	for {
		if err := json.Unmarshal(fleetRequest(t, srv, "GET",
			"/api/v1/admin/notifications", "admin", "", 200),
			&notifications); err != nil {
			t.Fatal(err)
		}
		if len(notifications) == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(notifications) != 2 || notifications[0].Type != "agent_recovered" ||
		notifications[1].Type != "agent_stale" {
		t.Fatalf("notification history order=%+v", notifications)
	}
}

func TestAgentFleetRepairedIdentityGetsHeartbeatGraceE2E(t *testing.T) {
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	if _, err := f.repo.DB.Exec(`UPDATE agents SET
		created_at=unixepoch()-3600, last_heartbeat_at=NULL,
		health_state='unknown' WHERE id='test-agent'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.Exec(`UPDATE agent_pairings
		SET paired_at=unixepoch() WHERE agent_id='test-agent'`); err != nil {
		t.Fatal(err)
	}
	if err := f.agents.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := fleetAgentState(t, srv); state.Agent.Health != "unknown" {
		t.Fatalf("fresh re-pair health=%s", state.Agent.Health)
	}
	if _, err := f.repo.DB.Exec(`UPDATE agent_pairings
		SET paired_at=unixepoch()-120 WHERE agent_id='test-agent'`); err != nil {
		t.Fatal(err)
	}
	if err := f.agents.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := fleetAgentState(t, srv); state.Agent.Health != "stale" {
		t.Fatalf("expired re-pair grace health=%s", state.Agent.Health)
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
