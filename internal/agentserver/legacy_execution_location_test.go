package agentserver_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestLegacyStartRecordsIdentityOnce(t *testing.T) {
	// Given: an older queued deployment claimed by a named agent.
	f := newAgentFixture(t)
	seedAgentLifecycle(t, f.repo)
	stream := f.broker.Subscribe(1)
	t.Cleanup(func() { f.broker.Unsubscribe(1, stream) })

	// When: its accepted start request is replayed.
	for range 2 {
		startLifecycle(t, f, http.StatusNoContent)
	}

	// Then: the server commits and broadcasts authenticated identity once.
	logs, err := f.repo.Queries.ListDeploymentLogsByDeployment(t.Context(), 1)
	if err != nil || len(logs) != 1 ||
		!strings.Contains(logs[0].Line, `agent="test" id="test-agent"`) {
		t.Fatalf("legacy identity logs=%+v error=%v", logs, err)
	}
	select {
	case line := <-stream:
		if line != logs[0].Line {
			t.Fatalf("streamed=%q stored=%q", line, logs[0].Line)
		}
	default:
		t.Fatal("legacy identity was not broadcast")
	}
	select {
	case line := <-stream:
		t.Fatalf("duplicate legacy identity: %q", line)
	default:
	}
}

func TestLegacyStartRollsBackWhenIdentityLogFails(t *testing.T) {
	// Given: identity cannot be saved for an older claim.
	f := newAgentFixture(t)
	seedAgentLifecycle(t, f.repo)
	if _, err := f.repo.DB.ExecContext(t.Context(), `
		CREATE TRIGGER reject_identity BEFORE INSERT ON deployment_logs
		BEGIN SELECT RAISE(ABORT, 'log unavailable'); END`); err != nil {
		t.Fatal(err)
	}

	// When: the authenticated agent asks to start.
	startLifecycle(t, f, http.StatusInternalServerError)

	// Then: neither the claim nor the deployment transitions to running.
	claim, err := f.repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != "claimed" || claim.StartedAt.Valid {
		t.Fatalf("claim escaped rollback: %+v error=%v", claim, err)
	}
	deployment, err := f.repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "pending" ||
		deployment.StartedAt.Valid {
		t.Fatalf("deployment escaped rollback: %+v error=%v", deployment, err)
	}
}
