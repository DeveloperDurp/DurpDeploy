//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestExecutionLocationAPIWebE2E(t *testing.T) {
	// Given: real server and agent execution against a local Podman socket.
	t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "")
	rawHost, err := exec.CommandContext(
		t.Context(),
		"podman",
		"--remote",
		"--url="+os.Getenv(
			"AGENT_TEST_SOCKET",
		),
		"info",
		"--format",
		"{{.Host.Hostname}}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimSpace(string(rawHost))
	if host == "" {
		t.Fatal("test runtime did not report its physical hostname")
	}
	s := newStagingE2E(t)
	decode := func(data []byte, value any) {
		t.Helper()
		if err := json.Unmarshal(data, value); err != nil {
			t.Fatal(err)
		}
	}
	var project, environment struct{ ID int64 }
	decode(s.api(t, "POST", "/projects", map[string]string{
		"name": "execution-location",
	}, 201), &project)
	decode(s.api(t, "POST", "/environments", map[string]string{
		"name": "execution-location",
	}, 201), &environment)
	for _, id := range []string{"test-agent", "second-agent"} {
		s.api(t, "POST", "/admin/agents/"+id+"/environments",
			map[string]int64{"environment_id": environment.ID}, 204)
		s.api(t, "POST", "/admin/agents/"+id+"/labels",
			map[string]string{"label": "execution-location"}, 204)
	}
	base := fmt.Sprintf("/projects/%d", project.ID)
	for _, step := range []map[string]any{
		{"name": "Server retry", "script_body": `if [ ! -e "$DURPDEPLOY_STAGE_DIR/once" ]; then touch "$DURPDEPLOY_STAGE_DIR/once"; exit 1; fi; echo server-output`, "container_image": "docker.io/library/bash:5.2", "max_retries": 1},
		{"name": "Agent shell", "script_body": "echo host-output", "execution_target": "agent", "agent_execution_mode": "host", "agent_selectors": []string{"test-agent"}},
		{"name": "Agent container", "script_body": "echo container-output", "execution_target": "agent", "agent_execution_mode": "container", "agent_selectors": []string{"second-agent"}, "container_image": "docker.io/library/bash:5.2"},
		{"name": "Fan-out", "script_body": "echo fanout-output", "execution_target": "agent", "agent_execution_mode": "container", "agent_selectors": []string{"execution-location"}, "container_image": "docker.io/library/bash:5.2"},
	} {
		s.api(t, "POST", base+"/steps", step, 201)
	}
	var release db.Release
	decode(s.api(t, "POST", base+"/releases",
		map[string]string{"version": "locations"}, 201), &release)

	// When: the public API runs the immutable release.
	var deployment db.Deployment
	decode(s.api(t, "POST", base+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": environment.ID,
	}, 201), &deployment)
	s.wait(t, deployment.ID, "succeeded")
	rawImage, err := exec.CommandContext(t.Context(), "podman", "--remote",
		"--url="+os.Getenv("AGENT_TEST_SOCKET"), "image", "inspect",
		"docker.io/library/bash:5.2", "--format", "{{.Id}}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	image := strings.TrimSpace(string(rawImage))

	// Then: all attempts and fan-out targets keep their location in public logs.
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	var logs []db.DeploymentLog
	decode(s.api(t, "GET", path+"/logs", nil, 200), &logs)
	counts := map[int64]int{}
	identities := map[int64]int{}
	for _, log := range logs {
		if strings.HasPrefix(log.Line, "Execution agent:") {
			identities[log.StepIndex.Int64]++
			continue
		}
		if !strings.HasPrefix(log.Line, "Execution location:") {
			continue
		}
		if !log.StepIndex.Valid ||
			!strings.Contains(log.Line, `host="`+host+`"`) {
			t.Fatalf("wrong execution host or step: %+v", log)
		}
		index := log.StepIndex.Int64
		counts[index]++
		if index != 0 && !strings.Contains(log.Line, `agent_id="`) {
			t.Fatalf(
				"location has no authenticated agent identity: %s",
				log.Line,
			)
		}
		mode := "mode=container"
		expectedImage := image
		if index == 0 {
			expectedImage = "docker.io/library/bash:5.2"
		}
		if index == 1 {
			mode = "mode=host/shell"
		}
		if !strings.Contains(log.Line, mode) ||
			!strings.Contains(log.Line, `interpreter="bash"`) ||
			(index != 1 && (!strings.Contains(log.Line, `container="durpdeploy-`) ||
				!strings.Contains(log.Line, fmt.Sprintf("image=%q", expectedImage)))) {
			t.Fatalf("incomplete execution location: %s", log.Line)
		}
		t.Logf("Public log location: step=%d line=%s", index, log.Line)
	}
	for index, want := range []int{2, 1, 1, 2} {
		if counts[int64(index)] != want {
			t.Fatalf(
				"step %d location count=%d want=%d",
				index,
				counts[int64(index)],
				want,
			)
		}
	}
	if identities[1] != 1 || identities[2] != 1 || identities[3] != 2 {
		t.Fatalf("agent identity counts=%v", identities)
	}
	for _, suffix := range []string{"/logs.txt", "/logs/stream?format=structured", "/logs/stream?format=ndjson"} {
		data := string(s.api(t, "GET", path+suffix, nil, 200))
		if strings.Count(data, "Execution location:") != 6 ||
			!strings.Contains(data, host) {
			t.Fatalf("execution locations missing from %s: %s", suffix, data)
		}
		t.Logf("Public log surface: %s locations=%d", suffix,
			strings.Count(data, "Execution location:"))
	}
	s.assertExecutionLocationWeb(t, deployment.ID)
}
