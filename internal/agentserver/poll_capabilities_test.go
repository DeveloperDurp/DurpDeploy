package agentserver_test

import (
	"net/http"
	"slices"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestPollPersistsContainerOnlyCapabilities(t *testing.T) {
	// Given
	f := newAgentFixture(t)
	id := seedPollPayload(t, f, "pending", "test-agent")
	if _, err := f.repo.DB.ExecContext(t.Context(),
		`UPDATE deployment_steps SET agent_execution_mode = 'container',
		 container_image = 'alpine:3.20' WHERE deployment_id = ?`, id,
	); err != nil {
		t.Fatal(err)
	}

	// When
	response := postAgent(t, f, agentproto.PollPath,
		`{"protocol":"agent/3","agent_version":"test-v3",`+
			`"supported_interpreters":[],"execution_modes":["container"],`+
			`"container_runtimes":["podman"],`+
			`"container_interpreters":["bash","python3","pwsh"]}`)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("poll status=%d", response.StatusCode)
	}
	assertAgentInterpreters(t, f, []string{})
	modes, err := f.repo.Queries.ListAgentExecutionModes(
		t.Context(),
		"test-agent",
	)
	if err != nil || !slices.Equal(modes, []string{"container"}) {
		t.Fatalf("modes=%v error=%v", modes, err)
	}
	runtimes, err := f.repo.Queries.ListAgentContainerRuntimes(
		t.Context(),
		"test-agent",
	)
	if err != nil || !slices.Equal(runtimes, []string{"podman"}) {
		t.Fatalf("runtimes=%v error=%v", runtimes, err)
	}
	interpreters, err := f.repo.Queries.ListAgentContainerInterpreters(
		t.Context(),
		"test-agent",
	)
	if err != nil ||
		!slices.Equal(interpreters, []string{"bash", "pwsh", "python3"}) {
		t.Fatalf("interpreters=%v error=%v", interpreters, err)
	}
}

func TestLegacyPollClearsContainerCapabilities(t *testing.T) {
	// Given
	f := newAgentFixture(t)
	seedPollPayload(t, f, "pending", "test-agent")
	if _, err := f.repo.DB.ExecContext(t.Context(), `
INSERT INTO agent_execution_modes VALUES('test-agent','container');
INSERT INTO agent_container_runtimes VALUES('test-agent','docker');
INSERT INTO agent_container_interpreters VALUES('test-agent','bash');`); err != nil {
		t.Fatal(err)
	}

	// When
	response := postAgent(t, f, agentproto.PollPath, pollBody)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("poll status=%d", response.StatusCode)
	}
	modes, err := f.repo.Queries.ListAgentExecutionModes(
		t.Context(),
		"test-agent",
	)
	if err != nil || !slices.Equal(modes, []string{"host"}) {
		t.Fatalf("modes=%v error=%v", modes, err)
	}
	runtimes, err := f.repo.Queries.ListAgentContainerRuntimes(
		t.Context(),
		"test-agent",
	)
	if err != nil || len(runtimes) != 0 {
		t.Fatalf("stale runtimes=%v error=%v", runtimes, err)
	}
	interpreters, err := f.repo.Queries.ListAgentContainerInterpreters(
		t.Context(),
		"test-agent",
	)
	if err != nil || len(interpreters) != 0 {
		t.Fatalf("stale interpreters=%v error=%v", interpreters, err)
	}
}
