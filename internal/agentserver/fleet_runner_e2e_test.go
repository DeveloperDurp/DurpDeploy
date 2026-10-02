package agentserver_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/db"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentFleetDrainPausesRunnerTimeoutE2E(t *testing.T) {
	// Given: a real API deployment with two one-second remote steps.
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	project, err := f.repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "drain-timeout"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := f.repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "drain-timeout"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := f.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: `[{"name":"first","script_body":"echo first","execution_target":"agent","timeout_seconds":1},{"name":"second","script_body":"echo second","execution_target":"agent","timeout_seconds":1}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/admin/agents/test-agent"
	fleetRequest(
		t,
		srv,
		"POST",
		base+"/environments",
		"admin",
		fmt.Sprintf(`{"environment_id":%d}`, environment.ID),
		204,
	)
	fleetRequest(t, srv, "POST", base+"/drain", "admin", "", 204)
	var deployment db.Deployment
	if err := json.Unmarshal(
		fleetRequest(
			t,
			srv,
			"POST",
			fmt.Sprintf("/api/v1/projects/%d/deployments", project.ID),
			"admin",
			fmt.Sprintf(
				`{"release_id":%d,"environment_id":%d}`,
				release.ID,
				environment.ID,
			),
			201,
		),
		&deployment,
	); err != nil {
		t.Fatal(err)
	}
	waitQueued := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if fleetAgentState(t, srv).Agent.Queued == 1 {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("runner did not queue its remote step")
	}
	for step := range 2 {
		waitQueued()
		// When: maintenance lasts beyond the configured execution timeout.
		time.Sleep(1500 * time.Millisecond)
		if state := fleetAgentState(t, srv); state.Agent.Queued != 1 {
			t.Fatalf("maintenance consumed step %d timeout: %+v", step, state)
		}
		fleetRequest(t, srv, "POST", base+"/resume", "admin", "", 204)
		poll := decodePollResponse(
			t,
			postAgent(t, f, agentproto.PollPath, pollBody),
		)
		if int64(poll.DeploymentID) != deployment.ID {
			t.Fatalf("claimed deployment=%d", poll.DeploymentID)
		}
		claim := fmt.Sprintf(
			`{"protocol":"agent/1","claim_token":%q}`,
			poll.ClaimToken,
		)
		if response := postAgent(
			t,
			f,
			fmt.Sprintf("/agent/v1/deployments/%d/start", deployment.ID),
			claim,
		); response.StatusCode != 204 {
			t.Fatal(response.StatusCode)
		}
		// Drain again: this issued step can finish, and the next step must wait.
		fleetRequest(t, srv, "POST", base+"/drain", "admin", "", 204)
		result := fmt.Sprintf(
			`{"protocol":"agent/1","claim_token":%q,"state":"succeeded"}`,
			poll.ClaimToken,
		)
		if response := postAgent(
			t,
			f,
			fmt.Sprintf("/agent/v1/deployments/%d/result", deployment.ID),
			result,
		); response.StatusCode != 204 {
			t.Fatal(response.StatusCode)
		}
	}
	// Then: both steps complete successfully through the public contracts.
	path := fmt.Sprintf("/api/v1/deployments/%d/status", deployment.ID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(
			fleetRequest(t, srv, "GET", path, "admin", "", 200),
			&status,
		); err != nil {
			t.Fatal(err)
		}
		if status.Status == "succeeded" {
			return
		}
		if status.Status == "failed" {
			t.Fatal("resumed deployment failed")
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("resumed deployment did not finish")
}
