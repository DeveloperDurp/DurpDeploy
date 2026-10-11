//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/pem"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"

	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
	"github.com/robfig/cron/v3"
)

type stagingE2E struct {
	f           agentFixture
	app         *httptest.Server
	token       string
	directories []string
}

func newStagingE2E(t *testing.T) *stagingE2E {
	t.Helper()
	binary := os.Getenv("DURPDEPLOY_AGENT_E2E_BINARY")
	if binary == "" {
		t.Fatal(
			"set DURPDEPLOY_AGENT_E2E_BINARY to the staging-capable agent binary",
		)
	}
	t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "podman")
	t.Setenv("DURPDEPLOY_CONTAINER_URL", os.Getenv("AGENT_TEST_SOCKET"))
	t.Setenv(
		"DURPDEPLOY_CONTAINER_NAMESPACE",
		fmt.Sprintf("stage-e2e-%d", time.Now().UnixNano()),
	)
	f := newAgentFixtureWithDSN(
		t,
		filepath.Join(
			t.TempDir(),
			"stage.db",
		)+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		nil,
	)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.repo.SetSecretBox(box)
	r := runner.New(f.repo, f.broker)
	t.Cleanup(r.KillAll)
	if !r.ContainerRuntimeReady() {
		t.Fatal("server container runtime is required")
	}
	user, err := f.repo.Queries.CreateUser(
		t.Context(),
		db.CreateUserParams{
			Email:        "staging@test.local",
			Name:         "Staging E2E",
			PasswordHash: "unused",
			Role:         "admin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{ID: "staging-e2e", UserID: user.ID, Name: "staging-e2e", TokenPrefix: prefix, TokenHash: hash, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(
		server.NewRouter(
			f.repo,
			r,
			cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
			handler.NewAuthHandler(f.repo),
		),
	)
	t.Cleanup(app.Close)
	s := &stagingE2E{f: f, app: app, token: token}
	s.startAgent(t, binary, "test-agent", f.identityDir)
	directory := t.TempDir()
	identity, err := agenttls.LoadOrCreate(directory, "https://second-agent")
	if err != nil {
		t.Fatal(err)
	}
	cert := string(
		pem.EncodeToMemory(
			&pem.Block{
				Type:  "CERTIFICATE",
				Bytes: identity.Certificate.Certificate[0],
			},
		),
	)
	if _, err := f.repo.DB.ExecContext(t.Context(), `INSERT INTO agents(id,name,endpoint,status,certificate_pem,certificate_fingerprint,encrypted_identity) VALUES('second-agent','second','https://second-agent','active',?,?,'fixture')`, cert, identity.Fingerprint.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.ExecContext(t.Context(), `INSERT INTO agent_pairings(agent_id,pairing_code_hash,agent_public_identity,agent_pin,state,expires_at,paired_at,server_public_identity,server_pin,encrypted_identity) VALUES('second-agent',randomblob(32),?,?,'paired',1,1,'fixture',?,'fixture')`, cert, identity.Fingerprint.String(), f.serverIdentity.Fingerprint.String()); err != nil {
		t.Fatal(err)
	}
	s.startAgent(t, binary, "second-agent", directory)
	for _, id := range []string{"test-agent", "second-agent"} {
		deadline := time.Now().Add(time.Minute)
		for {
			var count int
			if err := f.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM agent_staging_capabilities WHERE agent_id=?", id).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not advertise staging", id)
			}
			time.Sleep(100 * time.Millisecond)
		}
		s.api(
			t,
			"POST",
			"/admin/agents/"+id+"/labels",
			map[string]string{"label": id},
			204,
		)
	}
	return s
}

func (s *stagingE2E) startAgent(t *testing.T, binary, id, directory string) {
	t.Helper()
	state, err := agentstate.New(
		s.f.server.URL,
		[]agenttls.Fingerprint{s.f.serverIdentity.Fingerprint},
		id,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentstate.NewStore(directory).Save(state); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "LANG="+id)
	command.Env = append(
		command.Env,
		"DURPDEPLOY_AGENT_STATE_DIR="+directory,
		"DURPDEPLOY_AGENT_EXECUTION_BOUNDARY=service",
		"DURPDEPLOY_AGENT_VERSION=staging-e2e",
		"DURPDEPLOY_AGENT_CONTAINER_ENABLED=true",
		"DURPDEPLOY_AGENT_CONTAINER_RUNTIME=podman",
		"DURPDEPLOY_AGENT_CONTAINER_SOCKET="+os.Getenv("AGENT_TEST_SOCKET"),
	)
	log, err := os.Create(filepath.Join(t.TempDir(), id+".log"))
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Error(err)
		}
		if err := command.Wait(); err != nil {
			t.Error(err)
		}
		if err := log.Close(); err != nil {
			t.Error(err)
		}
		data, err := os.ReadFile(log.Name())
		if err != nil {
			t.Error(err)
		}
		if t.Failed() {
			t.Logf("agent %s: %s", id, data)
		}
	})
	s.directories = append(s.directories, directory)
}
