//go:build e2e

package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRunbookDeletionCleanupAPIWebE2E(t *testing.T) {
	for _, remote := range []string{"step", "deployment"} {
		t.Run(remote, func(t *testing.T) {
			checkRunbookDeletionCleanup(t, remote)
		})
	}
}

func checkRunbookDeletionCleanup(t *testing.T, remote string) {
	t.Helper()
	// Given: a failed parent with remote work whose cleanup is uncertain.
	f := newArtifactE2E(t)
	saved := createDeletionRunbook(t, f, "uncertain")
	execution, _, err := f.h.repo.CreateRunbookExecution(t.Context(),
		repository.RunbookExecutionRequest{
			ProjectID: f.project.ID, RunbookID: saved.Runbook.ID,
			EnvironmentID: f.environment.ID,
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{ID: execution.DeploymentID, Status: "failed"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateAgent(t.Context(),
		db.CreateAgentParams{ID: "cleanup", Name: "Cleanup"}); err != nil {
		t.Fatal(err)
	}
	table := "remote_step_runs"
	statement := `INSERT INTO remote_step_runs(deployment_id,step_index,agent_id,state)
VALUES(?,0,'cleanup','cleanup_unconfirmed')`
	if remote == "deployment" {
		table = "remote_deployment_claims"
		statement = `INSERT INTO remote_deployment_claims(deployment_id,agent_id,state,
claim_token_hash,ciphertext,claim_expires_at,last_heartbeat_at,started_at,finished_at)
VALUES(?,'cleanup','cleanup_unconfirmed',zeroblob(32),'fixture',1,1,1,1)`
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(), statement, execution.DeploymentID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/runbooks/%d", f.base(), saved.Runbook.ID)
	webPath := strings.TrimPrefix(path, "/api/v1") + "/delete"
	// When: deletion is requested while work or cleanup remains unconfirmed.
	for _, state := range []string{
		"claimed", "started", "cancel_requested", "lost",
		"cancel_unconfirmed", "cleanup_unconfirmed",
	} {
		started := sql.NullInt64{Int64: 1, Valid: state != "claimed"}
		finished := sql.NullInt64{Int64: 1, Valid: state == "lost" ||
			state == "cancel_unconfirmed" || state == "cleanup_unconfirmed"}
		cancelled := sql.NullInt64{
			Int64: 1,
			Valid: state == "cancel_requested" ||
				state == "cancel_unconfirmed",
		}
		if _, err := f.h.repo.DB.ExecContext(t.Context(), "UPDATE "+table+
			" SET state=?,started_at=?,finished_at=?,cancel_requested_at=?",
			state, started, finished, cancelled); err != nil {
			t.Fatal(err)
		}
		f.api(t, "DELETE", path, nil, http.StatusConflict)
		f.web(t, "POST", webPath, nil, http.StatusConflict)
	}
	// Then: confirmed remote cleanup permits deletion and retains its history.
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"UPDATE "+table+" SET cleanup_confirmed_at=1"); err != nil {
		t.Fatal(err)
	}
	f.api(t, "DELETE", path, nil, http.StatusNoContent)
	f.api(
		t,
		"GET",
		fmt.Sprintf("%s/runbook-executions/%d", f.base(), execution.ID),
		nil,
		http.StatusOK,
	)
	var count int
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM "+table+" WHERE deployment_id=?", execution.DeploymentID,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cleanup history count=%d error=%v", count, err)
	}
}
