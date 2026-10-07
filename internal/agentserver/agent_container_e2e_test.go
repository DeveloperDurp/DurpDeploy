//go:build e2e && agentcontainer

package agentserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
	"github.com/robfig/cron/v3"
)

func TestAgentContainerAPIE2E(t *testing.T) {
	// Real public API, mTLS polling, pinned agent image, and mounted local engine socket.
	runtime := os.Getenv("AGENT_TEST_RUNTIME")
	image := os.Getenv("AGENT_TEST_IMAGE")
	socket, err := url.Parse(os.Getenv("AGENT_TEST_SOCKET"))
	if err != nil || socket.Scheme != "unix" ||
		(runtime != "podman" && runtime != "docker") ||
		image == "" {
		t.Fatal(
			"set AGENT_TEST_RUNTIME, AGENT_TEST_SOCKET, and AGENT_TEST_IMAGE",
		)
	}
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv(
		"DURPDEPLOY_CONTAINER_NAMESPACE",
		fmt.Sprintf("issue99-api-%d", os.Getpid()),
	)
	f := newAgentFixtureWithDSN(t, filepath.Join(t.TempDir(), "agent.db")+
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", nil)
	state, err := agentstate.New(
		f.server.URL,
		[]agenttls.Fingerprint{f.serverIdentity.Fingerprint},
		"test-agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentstate.NewStore(f.identityDir).Save(state); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("issue99-agent-%s-%d", runtime, os.Getpid())
	clientArgs := []string{"--host=" + socket.String()}
	if runtime == "podman" {
		clientArgs = []string{"--remote", "--url=" + socket.String()}
	}
	args := append(
		clientArgs,
		"run",
		"--detach",
		"--name="+name,
		"--network=host",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--security-opt=label=disable",
		fmt.Sprintf("--user=%d:%d", os.Getuid(), os.Getgid()),
		"--tmpfs=/tmp:size=64m,mode=1777",
		"--volume="+f.identityDir+":/var/lib/durpdeploy-agent",
		"--volume="+socket.Path+":/run/durpdeploy/runtime.sock:ro",
		"--env=DURPDEPLOY_AGENT_CONTAINER_ENABLED=true",
		"--env=DURPDEPLOY_AGENT_CONTAINER_RUNTIME="+runtime,
		"--env=DURPDEPLOY_AGENT_CONTAINER_SOCKET=unix:///run/durpdeploy/runtime.sock",
	)
	if runtime == "podman" {
		args = append(args, "--userns=keep-id")
	}
	info, err := os.Stat(socket.Path)
	if err != nil {
		t.Fatal(err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		args = append(args, fmt.Sprintf("--group-add=%d", stat.Gid))
	}
	args = append(args, image)
	if output, err := exec.Command(runtime, args...).CombinedOutput(); err != nil {
		t.Fatalf("start agent: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if t.Failed() {
			output, _ := exec.Command(runtime, append(clientArgs, "logs", name)...).
				CombinedOutput()
			t.Logf("agent: %s", output)
		}
		if output, err := exec.Command(runtime, append(clientArgs, "rm", "--force", "--volumes", name)...).CombinedOutput(); err != nil {
			t.Errorf("remove agent: %v: %s", err, output)
		}
	})
	deadline := time.Now().Add(time.Minute)
	for {
		agent, err := f.repo.Queries.GetAgent(t.Context(), "test-agent")
		if err != nil {
			t.Fatal(err)
		}
		if agent.AgentProtocol.String == "agent/3" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent did not report v3 readiness")
		}
		time.Sleep(100 * time.Millisecond)
	}
	user, err := f.repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: "container@test.local", Name: "Container E2E", PasswordHash: "unused", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{
		ID: "container-e2e", UserID: user.ID, Name: "container-e2e", TokenPrefix: prefix, TokenHash: hash, Scope: "global",
	}); err != nil {
		t.Fatal(err)
	}
	deploymentRunner := runner.New(f.repo, f.broker)
	t.Cleanup(deploymentRunner.KillAll)
	app := httptest.NewServer(server.NewRouter(
		f.repo,
		deploymentRunner,
		cron.NewParser(
			cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
		),
		handler.NewAuthHandler(f.repo),
	))
	t.Cleanup(app.Close)
	request := func(method, path string, body any, status int) []byte {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(
			t.Context(),
			method,
			app.URL+"/api/v1"+path,
			bytes.NewReader(raw),
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf(
				"%s %s status=%d error=%v body=%s",
				method,
				path,
				response.StatusCode,
				err,
				data,
			)
		}
		return data
	}
	var project, environment struct{ ID int64 }
	if err := json.Unmarshal(request("POST", "/projects", map[string]string{"name": "remote-container"}, 201), &project); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(request("POST", "/environments", map[string]string{"name": "remote-container"}, 201), &environment); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.AddAgentEnvironmentLabel(t.Context(), db.AddAgentEnvironmentLabelParams{
		AgentID: "test-agent", EnvironmentID: environment.ID,
	}); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/projects/%d", project.ID)
	request(
		"POST",
		base+"/variables",
		map[string]string{"name": "SELECTED", "value": "selected"},
		201,
	)
	request(
		"POST",
		base+"/variables",
		map[string]string{"name": "UNRELATED", "value": "unrelated"},
		201,
	)
	for _, step := range []struct{ interpreter, image, script string }{
		{"bash", "docker.io/library/bash:5.2", `test "$SELECTED" = selected; test "${UNRELATED-unset}" = unset; printf 'bash-ok\n'`},
		{"python3", "docker.io/library/python:3.12-alpine", `import os; assert os.environ['SELECTED']=='selected'; assert 'UNRELATED' not in os.environ; print('python-ok')`},
		{"pwsh", "mcr.microsoft.com/powershell:latest", `if ($env:SELECTED -ne 'selected' -or $env:UNRELATED) { exit 1 }; Write-Output 'pwsh-ok'`},
	} {
		request("POST", base+"/steps", map[string]any{
			"name": step.interpreter, "script_body": step.script, "interpreter": step.interpreter,
			"execution_target": "agent", "agent_execution_mode": "container", "container_image": step.image,
			"variable_names": []string{"SELECTED"}, "timeout_seconds": 120,
		}, 201)
	}
	var release, deployment struct{ ID int64 }
	if err := json.Unmarshal(request("POST", base+"/releases", map[string]string{"version": "v1"}, 201), &release); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(request("POST", base+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": environment.ID,
	}, 201), &deployment); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(4 * time.Minute)
	for {
		var result struct{ Status string }
		if err := json.Unmarshal(request("GET", fmt.Sprintf("/deployments/%d", deployment.ID), nil, 200), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status == "succeeded" {
			break
		}
		if result.Status == "failed" ||
			result.Status == "cleanup_unconfirmed" ||
			time.Now().After(deadline) {
			t.Fatalf("container deployment status=%s", result.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs := request(
		"GET",
		fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
		nil,
		200,
	)
	for _, marker := range []string{"bash-ok", "python-ok", "pwsh-ok"} {
		if !strings.Contains(string(logs), marker) {
			t.Fatalf("missing %s in deployment logs", marker)
		}
	}
	// A later edit cannot change the release/deployment's execution mode.
	request(
		"PUT",
		base+"/steps/1",
		map[string]string{
			"name":             "host",
			"script_body":      "echo host",
			"execution_target": "agent",
		},
		200,
	)
	snapshots, err := f.repo.Queries.ListDeploymentSteps(
		t.Context(),
		deployment.ID,
	)
	if err != nil || len(snapshots) != 3 ||
		snapshots[0].AgentExecutionMode != "container" {
		t.Fatalf("snapshots=%+v error=%v", snapshots, err)
	}
}
