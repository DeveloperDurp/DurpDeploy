package agentserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRemoteStepStartLogsAgentIdentityOnce(t *testing.T) {
	for _, mode := range []string{"host", "container"} {
		t.Run(mode, func(t *testing.T) {
			// Given: a claimed step on a named agent.
			fixture := newAgentFixture(t)
			poll := pollBody
			if mode == "container" {
				poll = containerPollBody
			}
			id, claim := claimedRemoteStepWithMode(t, fixture, mode, poll)
			if _, err := fixture.repo.DB.ExecContext(t.Context(),
				`UPDATE agents SET name = 'Production runner' WHERE id = 'test-agent'`,
			); err != nil {
				t.Fatal(err)
			}
			stream := fixture.broker.Subscribe(id)
			t.Cleanup(func() { fixture.broker.Unsubscribe(id, stream) })
			path := strings.ReplaceAll(
				agentproto.StartPath,
				"{id}",
				fmt.Sprint(id),
			)

			// When: the agent retries its accepted start request.
			for range 2 {
				response := postAgent(t, fixture, path, claim)
				if response.StatusCode != http.StatusNoContent {
					t.Fatalf("start status=%d", response.StatusCode)
				}
			}

			// Then: its identity is persisted and broadcast once in this step.
			logs, err := fixture.repo.Queries.ListDeploymentLogsByDeployment(
				t.Context(), id,
			)
			if err != nil || len(logs) != 1 {
				t.Fatalf("start logs=%+v error=%v", logs, err)
			}
			log := logs[0]
			if !strings.Contains(log.Line, `agent="Production runner"`) ||
				!strings.Contains(log.Line, `id="test-agent"`) ||
				!log.StepIndex.Valid || log.StepIndex.Int64 != 0 {
				t.Fatalf("agent identity missing or in wrong step: %+v", log)
			}
			select {
			case line := <-stream:
				if line != log.Line {
					t.Fatalf("streamed=%q saved=%q", line, log.Line)
				}
			default:
				t.Fatal("agent identity was not broadcast")
			}
			select {
			case line := <-stream:
				t.Fatalf("duplicate start log: %q", line)
			default:
			}
		})
	}
}

func TestRemoteStepStartRollsBackWhenIdentityLogFails(t *testing.T) {
	// Given: an issued claim whose log cannot be saved.
	fixture := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, fixture)
	if _, err := fixture.repo.DB.ExecContext(t.Context(), `
		CREATE TRIGGER reject_identity_log BEFORE INSERT ON deployment_logs
		BEGIN SELECT RAISE(ABORT, 'log storage unavailable'); END`,
	); err != nil {
		t.Fatal(err)
	}

	// When: the agent asks to start.
	path := strings.ReplaceAll(agentproto.StartPath, "{id}", fmt.Sprint(id))
	response := postAgent(t, fixture, path, claim)

	// Then: execution remains claimed so it cannot run without a saved identity.
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	runs, err := fixture.repo.Queries.ListRemoteStepRuns(t.Context(),
		db.ListRemoteStepRunsParams{DeploymentID: id, StepIndex: 0},
	)
	if err != nil || len(runs) != 1 || runs[0].State != "claimed" ||
		runs[0].StartedAt.Valid {
		t.Fatalf(
			"start escaped failed log transaction: runs=%+v error=%v",
			runs,
			err,
		)
	}
}

func TestRemoteStepStartRedactsAgentName(t *testing.T) {
	// Given: an agent name containing a snapshotted secret and a newline.
	fixture := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, fixture)
	deployment, err := fixture.repo.Queries.GetDeployment(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.DB.ExecContext(t.Context(), `
		INSERT INTO release_variables (release_id,name,value,secret)
		VALUES (?,'TOKEN','step-secret',1)`, deployment.ReleaseID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.DB.ExecContext(t.Context(),
		`UPDATE agents SET name = ? WHERE id = 'test-agent'`,
		"runner step-secret\nsecond line",
	); err != nil {
		t.Fatal(err)
	}

	// When: the accepted start records the name.
	path := strings.ReplaceAll(agentproto.StartPath, "{id}", fmt.Sprint(id))
	response := postAgent(t, fixture, path, claim)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("start status=%d", response.StatusCode)
	}

	// Then: the stored metadata redacts the secret and escapes the newline.
	logs, err := fixture.repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(),
		id,
	)
	if err != nil || len(logs) != 1 {
		t.Fatalf("start logs=%+v error=%v", logs, err)
	}
	if strings.Contains(logs[0].Line, "step-secret") ||
		strings.ContainsAny(logs[0].Line, "\r\n") ||
		!strings.Contains(logs[0].Line, "[REDACTED]") {
		t.Fatalf("unsafe execution metadata: %q", logs[0].Line)
	}
}
