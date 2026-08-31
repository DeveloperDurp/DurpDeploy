package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"durpdeploy/internal/agentpayload"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

func TestAgentSubprocess_completesOrderedStepsAndRedactsSecrets(t *testing.T) {
	// Given
	fixture := newAgentSubprocessFixture(
		t,
		`printf 'first\n'; printf '%s\n' "$SECRET"; printf 'second\n'`,
	)
	process := fixture.start(t)

	// When
	result := <-fixture.result
	process.Process.Signal(syscall.SIGTERM)
	if err := process.Wait(); err != nil {
		t.Fatalf("wait agent: %v", err)
	}

	// Then
	if result.State != agentproto.ResultSucceeded || result.Error != "" {
		t.Fatalf("result = %#v", result)
	}
	if got := strings.Join(
		fixture.logs(),
		"\n",
	); got != "first\n[REDACTED]\nsecond" {
		t.Fatalf("logs = %q", got)
	}
	fixture.assertNoSecretFiles(t)
}

func TestAgentSubprocess_sigtermKillsSpawnedChild(t *testing.T) {
	// Given
	pidDir := t.TempDir()
	if err := os.Chmod(pidDir, 0o777); err != nil {
		t.Fatalf("make PID directory writable: %v", err)
	}
	pidPath := filepath.Join(pidDir, "child.pid")
	fixture := newAgentSubprocessFixture(
		t,
		`sleep 30 & echo $! > "$PID_FILE"; wait`,
	)
	fixture.addVariable(variablePayload{Name: "PID_FILE", Value: pidPath})
	process := fixture.start(t)
	pid := waitForPID(t, pidPath)

	// When
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal agent: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("wait agent: %v", err)
	}

	// Then
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child PID %d remains after agent SIGTERM: %v", pid, err)
	}
}

func TestAgentSubprocess_pollsAgainAfterStaleStart(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "executed")
	fixture := newAgentSubprocessFixture(t, `touch "$MARKER"`)
	fixture.addVariable(variablePayload{Name: "MARKER", Value: marker})
	fixture.startConflict = true
	process := fixture.start(t)

	// When
	select {
	case <-fixture.pollAgain:
	case <-time.After(5 * time.Second):
		process.Process.Signal(syscall.SIGTERM)
		_ = process.Wait()
		t.Fatal("agent did not poll again after stale start")
	}
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal agent: %v", err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("wait agent: %v", err)
	}

	// Then
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale claim ran deployment script: %v", err)
	}
}

type agentSubprocessFixture struct {
	t             *testing.T
	server        *httptest.Server
	stateDir      string
	payload       deploymentPayload
	identity      agenttls.Identity
	serverID      agenttls.Identity
	mu            sync.Mutex
	logEvents     []agentproto.LogEvent
	result        chan agentproto.ResultRequest
	shutdown      context.Context
	cancel        context.CancelFunc
	pollServed    bool
	startConflict bool
	pollAgain     chan struct{}
	pollAgainOnce sync.Once
}

func newAgentSubprocessFixture(
	t *testing.T,
	script string,
) *agentSubprocessFixture {
	t.Helper()
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("secure state dir: %v", err)
	}
	identity, err := agenttls.LoadOrCreate(stateDir, "https://127.0.0.1")
	if err != nil {
		t.Fatalf("create agent identity: %v", err)
	}
	serverID, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	shutdown, cancel := context.WithCancel(context.Background())
	fixture := &agentSubprocessFixture{
		t: t, stateDir: stateDir, identity: identity, serverID: serverID,
		shutdown: shutdown, cancel: cancel,
		payload: deploymentPayload{DeploymentID: 42,
			Release: releasePayload{
				ID:        1,
				ProjectID: 1,
				Version:   "v1",
				Steps: []stepPayload{
					{Name: "deploy", ScriptBody: script, SortOrder: 1},
				},
			},
			Environment: environmentPayload{ID: 1, Name: "test"},
			Variables: []variablePayload{
				{Name: "SECRET", Value: "subprocess-secret", Secret: true},
			}},
		result:    make(chan agentproto.ResultRequest, 1),
		pollAgain: make(chan struct{}),
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.handle))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverID.Certificate},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequestClientCert,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	t.Cleanup(cancel)
	fixture.server = server
	return fixture
}

func (fixture *agentSubprocessFixture) handle(
	writer http.ResponseWriter,
	request *http.Request,
) {
	switch request.URL.Path {
	case "/agent/v1/deployments/42/start":
		fixture.mu.Lock()
		conflict := fixture.startConflict
		fixture.mu.Unlock()
		if conflict {
			writer.WriteHeader(http.StatusConflict)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	case "/agent/v1/deployments/42/cancelled":
		writer.WriteHeader(http.StatusNoContent)
	case agentproto.PollPath:
		fixture.mu.Lock()
		served := fixture.pollServed
		fixture.pollServed = true
		payload := fixture.payload
		payload.Variables = append(
			[]variablePayload(nil),
			fixture.payload.Variables...)
		fixture.mu.Unlock()
		if served {
			fixture.pollAgainOnce.Do(func() { close(fixture.pollAgain) })
			select {
			case <-request.Context().Done():
			case <-fixture.shutdown.Done():
			}
			return
		}
		raw, _ := json.Marshal(payload)
		envelope, _ := agentpayload.Seal(
			fixture.identity.Certificate.Certificate[0],
			42,
			raw,
		)
		_ = json.NewEncoder(writer).
			Encode(agentproto.PollResponse{DeploymentID: 42, Payload: string(envelope), ClaimToken: "test-claim"})
	case "/agent/v1/deployments/42/logs":
		var batch agentproto.LogBatchRequest
		_ = json.NewDecoder(request.Body).Decode(&batch)
		fixture.mu.Lock()
		fixture.logEvents = append(fixture.logEvents, batch.Events...)
		fixture.mu.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	case "/agent/v1/deployments/42/result":
		var result agentproto.ResultRequest
		_ = json.NewDecoder(request.Body).Decode(&result)
		fixture.result <- result
		writer.WriteHeader(http.StatusNoContent)
	case "/agent/v1/deployments/42/heartbeat":
		_ = json.NewEncoder(writer).Encode(agentproto.HeartbeatResponse{})
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

func (fixture *agentSubprocessFixture) addVariable(variable variablePayload) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.payload.Variables = append(fixture.payload.Variables, variable)
}

func (fixture *agentSubprocessFixture) start(t *testing.T) *exec.Cmd {
	t.Helper()
	state, err := agentstate.New(
		fixture.server.URL,
		[]agenttls.Fingerprint{fixture.serverID.Fingerprint},
		"agent-test",
	)
	if err != nil {
		t.Fatalf("create paired state: %v", err)
	}
	if err := agentstate.NewStore(fixture.stateDir).Save(state); err != nil {
		t.Fatalf("save paired state: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "durpdeploy-agent")
	build := exec.Command(
		"go",
		"build",
		"-tags",
		"agenttest",
		"-o",
		binary,
		".",
	)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agent: %v: %s", err, output)
	}
	command := exec.Command(binary)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"DURPDEPLOY_AGENT_STATE_DIR=" + fixture.stateDir,
		"DURPDEPLOY_AGENT_VERSION=test",
	}
	for _, value := range command.Env {
		if strings.HasPrefix(value, "DURPDEPLOY_SECRET_KEY=") {
			t.Fatal("agent received server secret key")
		}
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}
	return command
}

func (fixture *agentSubprocessFixture) logs() []string {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	lines := make([]string, len(fixture.logEvents))
	for index, event := range fixture.logEvents {
		if event.Sequence != agentproto.LogSequence(index+1) {
			fixture.t.Fatalf("log sequence %d = %d", index, event.Sequence)
		}
		lines[index] = event.Line
	}
	return lines
}
