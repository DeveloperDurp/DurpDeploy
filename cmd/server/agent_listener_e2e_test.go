package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/events"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

type agentE2ENotifier struct {
	completed chan<- events.Event
}

func (n agentE2ENotifier) Name() string { return "agent-e2e" }

func (n agentE2ENotifier) Notify(
	_ context.Context,
	event events.Event,
) (bool, error) {
	n.completed <- event
	return true, nil
}

func TestAgentListener_remoteAgentCompletesRemoteDispatch(t *testing.T) {
	testAgentListenerRemoteAgentCompletesRemoteDispatch(t, tempDSN(t))
}

func testAgentListenerRemoteAgentCompletesRemoteDispatch(
	t *testing.T,
	dsn string,
) {
	// Given
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("secret box: %v", err)
	}
	broker := runner.NewLogBroker()
	completed := make(chan events.Event, 1)
	bus := events.NewBus(repo)
	bus.Register(agentE2ENotifier{completed: completed})
	listener, err := startAgentListener(
		agentListenerConfig{
			addr:        "127.0.0.1:0",
			publicURL:   "https://127.0.0.1",
			identityDir: t.TempDir(),
		},
		agentListenerDependencies{
			repo:   repo,
			box:    box,
			broker: broker,
			bus:    bus,
		},
	)
	if err != nil {
		t.Fatalf("start agent listener: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer shutdownCancel()
		if shutdownErr := listener.shutdown(shutdownCtx); shutdownErr != nil {
			t.Errorf("shutdown agent listener: %v", shutdownErr)
		}
	})
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "agent-e2e-project"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "agent-e2e-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "agent-e2e-v1",
			StepsJson: `[{"name":"remote","script_body":` +
				`"printf 'remote-listener-e2e\\n'","sort_order":0}]`,
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	pool, err := repo.Queries.CreateAgentPool(
		ctx,
		db.CreateAgentPoolParams{Name: "agent-e2e-pool"},
	)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := repo.Queries.UpsertEnvironmentAgentPolicy(
		ctx,
		db.UpsertEnvironmentAgentPolicyParams{
			EnvironmentID: environment.ID,
			PoolID:        pool.ID,
		},
	); err != nil {
		t.Fatalf("set environment policy: %v", err)
	}
	if _, err := repo.Queries.CreatePendingAgent(
		ctx,
		db.CreatePendingAgentParams{
			ID:   "agent-e2e",
			Name: "agent e2e",
		},
	); err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	if _, err := repo.DB.ExecContext(
		ctx,
		"INSERT INTO agent_pool_memberships (pool_id, agent_id) VALUES (?, ?)",
		pool.ID,
		"agent-e2e",
	); err != nil {
		t.Fatalf("add agent to pool: %v", err)
	}
	const enrollmentToken = "agent-e2e-enrollment-token"
	tokenHash := sha256.Sum256([]byte(enrollmentToken))
	if err := repo.Queries.CreateAgentEnrollmentToken(
		ctx,
		db.CreateAgentEnrollmentTokenParams{
			TokenHash:   tokenHash[:],
			TokenPrefix: "agent-e2e",
			AgentID:     "agent-e2e",
			ExpiresAt:   time.Now().Add(time.Minute).Unix(),
		},
	); err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	dispatcher := dispatch.New(repo, box, nil)
	if err := dispatcher.Dispatch(ctx, deployment.ID); err != nil {
		t.Fatalf("dispatch deployment: %v", err)
	}
	route, err := repo.Queries.GetDeploymentDispatch(ctx, deployment.ID)
	if err != nil || route.Mode != "remote" || route.State != "waiting" {
		t.Fatalf("remote route = %#v, %v", route, err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "durpdeploy-agent")
	build := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-o",
		binary,
		"./cmd/agent",
	)
	build.Dir = root
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("build agent: %v: %s", buildErr, output)
	}
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("secure agent state directory: %v", err)
	}
	address := listener.listener.Addr().String()
	var output bytes.Buffer
	agent := exec.Command(binary)
	agent.Stdout = &output
	agent.Stderr = &output
	agent.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"DURPDEPLOY_AGENT_SERVER_URL=https://" + address,
		"DURPDEPLOY_AGENT_SERVER_FINGERPRINT=" +
			listener.identity.Fingerprint.String(),
		"DURPDEPLOY_AGENT_STATE_DIR=" + stateDir,
		"DURPDEPLOY_AGENT_ENROLLMENT_TOKEN=" + enrollmentToken,
		"DURPDEPLOY_AGENT_ID=agent-e2e",
		"DURPDEPLOY_AGENT_NAME=agent e2e",
		"DURPDEPLOY_AGENT_VERSION=e2e",
	}
	if err := agent.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}
	t.Cleanup(func() {
		if agent.ProcessState == nil {
			_ = agent.Process.Signal(syscall.SIGTERM)
			_ = agent.Wait()
		}
	})

	// When
	select {
	case event := <-completed:
		if event.Type != events.DeploymentSucceeded ||
			event.DeploymentID != deployment.ID {
			t.Fatalf("completion event = %#v", event)
		}
	case <-ctx.Done():
		t.Fatalf(
			"remote deployment did not complete: %v; agent output: %s",
			ctx.Err(),
			output.String(),
		)
	}
	if err := agent.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("stop agent: %v", err)
	}
	if err := agent.Wait(); err != nil {
		t.Fatalf("wait for agent: %v: %s", err, output.String())
	}

	// Then
	finished, err := repo.Queries.GetDeployment(ctx, deployment.ID)
	if err != nil || finished.Status != "succeeded" {
		t.Fatalf("deployment = %#v, %v", finished, err)
	}
	route, err = repo.Queries.GetDeploymentDispatch(ctx, deployment.ID)
	if err != nil || route.State != "succeeded" ||
		route.AgentID.String != "agent-e2e" {
		t.Fatalf("dispatch = %#v, %v", route, err)
	}
	var logs []string
	if err := repo.ForEachDeploymentLogByDeploymentAsc(
		ctx,
		deployment.ID,
		func(log db.DeploymentLog) error {
			logs = append(logs, log.Line)
			return nil
		},
	); err != nil {
		t.Fatalf("list deployment logs: %v", err)
	}
	if !slices.Contains(logs, "remote-listener-e2e") {
		t.Fatalf("deployment logs = %q", logs)
	}
	registered, err := repo.Queries.GetAgent(ctx, "agent-e2e")
	if err != nil || registered.Status != "active" {
		t.Fatalf("enrolled agent = %#v, %v", registered, err)
	}
}
