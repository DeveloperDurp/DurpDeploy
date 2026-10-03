package agentserver_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentFleetAssignmentsE2E(t *testing.T) {
	// Given: a real admin API and an existing environment.
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	environment, err := f.repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{
			Name: "fleet assignment",
			Description: sql.NullString{
				String: "Production routing",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"environment_id":%d}`, environment.ID)
	path := "/api/v1/admin/agents/test-agent/environments"

	// When: the administrator changes routing labels through the API.
	fleetRequest(t, srv, "POST", path, "admin", body, 204)
	fleetRequest(t, srv, "POST", path, "admin", body, 204)
	var detail struct {
		Environments []struct {
			ID          int64   `json:"id"`
			Description *string `json:"description"`
			Tags        *string `json:"tags"`
		} `json:"environment_labels"`
	}
	if err := json.Unmarshal(
		fleetRequest(
			t,
			srv,
			"GET",
			"/api/v1/admin/agents/test-agent",
			"admin",
			"",
			200,
		),
		&detail,
	); err != nil {
		t.Fatal(err)
	}
	if len(detail.Environments) != 1 ||
		detail.Environments[0].ID != environment.ID {
		t.Fatalf("environment labels=%+v", detail.Environments)
	}
	if detail.Environments[0].Description == nil ||
		*detail.Environments[0].Description != "Production routing" ||
		detail.Environments[0].Tags != nil {
		t.Fatalf("environment wire contract=%+v", detail.Environments[0])
	}
	missing := fleetRequest(
		t,
		srv,
		"POST",
		path,
		"admin",
		`{"environment_id":999999}`,
		404,
	)
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(
		missing,
		&failure,
	); err != nil ||
		failure.Error != "Environment not found" {
		t.Fatalf("missing environment=%s err=%v", missing, err)
	}
	fleetRequest(t, srv, "DELETE", path, "admin", body, 204)
	fleetRequest(t, srv, "POST", path, "admin", `{"environment_id":0}`, 400)
	fleetRequest(t, srv, "POST", path, "viewer", body, 403)

	// Then: only real changes are audited, with the agent and environment IDs.
	var entries struct {
		Items []db.AuditLog `json:"items"`
	}
	if err := json.Unmarshal(
		fleetRequest(t, srv, "GET", "/api/v1/admin/audit", "admin", "", 200),
		&entries,
	); err != nil {
		t.Fatal(err)
	}
	assignments := 0
	for _, entry := range entries.Items {
		if entry.Action != "add_agent_environment_label" &&
			entry.Action != "delete_agent_environment_label" {
			continue
		}
		var details struct {
			Agent       string `json:"agent_id"`
			Environment int64  `json:"environment_id"`
		}
		if err := json.Unmarshal(
			[]byte(entry.Details.String),
			&details,
		); err != nil {
			t.Fatal(err)
		}
		if details.Agent != "test-agent" ||
			details.Environment != environment.ID {
			t.Fatalf("audit details=%+v", details)
		}
		assignments++
	}
	if assignments != 2 {
		t.Fatalf("assignment audits=%d", assignments)
	}
}

func TestAgentFleetLastErrorE2E(t *testing.T) {
	// Given: a claimed remote step.
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	deploymentID, claim := claimedRemoteStep(t, f)
	startPath := fmt.Sprintf("/agent/v1/deployments/%d/start", deploymentID)
	if res := postAgent(t, f, startPath, claim); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}

	// When: its failure is reported through the real agent result endpoint.
	var token struct {
		Token string `json:"claim_token"`
	}
	if err := json.Unmarshal([]byte(claim), &token); err != nil {
		t.Fatal(err)
	}
	result := fmt.Sprintf(
		`{"protocol":"agent/1","claim_token":%q,"state":"failed","error":"untrusted diagnostic"}`,
		token.Token,
	)
	if res := postAgent(
		t,
		f,
		fmt.Sprintf("/agent/v1/deployments/%d/result", deploymentID),
		result,
	); res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}

	// Then: the API exposes a stable failure, not raw agent-supplied text.
	var detail struct {
		Agent struct {
			Error *struct {
				ID     int64  `json:"deployment_id"`
				Reason string `json:"reason"`
			} `json:"last_error"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(
		fleetRequest(
			t,
			srv,
			"GET",
			"/api/v1/admin/agents/test-agent",
			"admin",
			"",
			200,
		),
		&detail,
	); err != nil {
		t.Fatal(err)
	}
	if detail.Agent.Error == nil || detail.Agent.Error.ID != deploymentID ||
		detail.Agent.Error.Reason != "failed" {
		t.Fatalf("last error=%+v", detail)
	}
}
