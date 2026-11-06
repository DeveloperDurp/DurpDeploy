package repository_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"durpdeploy/internal/logscrub"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRemoteLogReceiptDatabase(t *testing.T) {
	runRemoteLogReceiptParity(t, remoteFixture(t))
}

func runRemoteLogReceiptParity(t *testing.T, repo *repository.Repository) {
	t.Helper()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	for _, step := range []bool{false, true} {
		runRemoteReceiptClaim(t, repo, box, step)
	}
}

func runRemoteReceiptClaim(
	t *testing.T, repo *repository.Repository, box *secret.Box, step bool,
) {
	t.Helper()
	scrubber := logscrub.New([]string{"hidden-end"})
	identity, stepLabel := startRemoteReceiptClaim(t, repo, step, scrubber)
	appendLogs := func(events []repository.RemoteLogEvent) (int, error) {
		if step {
			logs, handled, err := repo.AppendRemoteStepLogs(
				t.Context(),
				identity,
				events,
				scrubber,
			)
			if !handled {
				t.Fatal("step claim was not handled")
			}
			return len(logs), err
		}
		logs, err := repo.AppendRemoteDeploymentLogs(
			t.Context(),
			identity,
			events,
			scrubber,
		)
		return len(logs), err
	}
	query := "SELECT log_buffer_ciphertext FROM remote_deployment_claims WHERE deployment_id=?"
	if step {
		query = "SELECT log_buffer_ciphertext FROM remote_step_runs WHERE deployment_id=?"
	}
	readState := func() (int64, bool) {
		t.Helper()
		return readRemoteReceiptState(
			t,
			repo,
			box,
			query,
			identity.DeploymentID,
		)
	}
	assertInitialRemoteReceipt(t, appendLogs)
	assertRemoteReceiptCursor(t, readState, 1, false)
	assertActiveReceiptFlushRejected(t, repo, identity, step, scrubber)
	assertRemoteReceiptCursor(t, readState, 1, false)
	// Recreate the repository to reload receipt state from encrypted storage.
	repo = repository.New(repo.DB)
	repo.SetSecretBox(box)
	if n, err := appendLogs([]repository.RemoteLogEvent{{Sequence: 1, Line: "changed duplicate"}}); err != nil ||
		n != 0 {
		t.Fatalf("duplicate: %d %v", n, err)
	}
	fillRemoteReceiptTail(t, appendLogs)
	assertRemoteReceiptCursor(t, readState, agentproto.MaxLogEvents+2, true)
	repo = repository.New(repo.DB)
	repo.SetSecretBox(box)
	if n, err := appendLogs([]repository.RemoteLogEvent{{Sequence: agentproto.MaxLogEvents + 3, Line: "end safe text"}}); err != nil ||
		n != 1 {
		t.Fatalf("suppressed continuation: %d %v", n, err)
	}
	finishRemoteReceiptClaim(t, repo, identity, step, scrubber)
	var terminal sql.NullString
	if err := repo.DB.QueryRowContext(t.Context(), query, identity.DeploymentID).Scan(&terminal); err != nil ||
		terminal.Valid {
		t.Fatalf("terminal state: %v", err)
	}
	assertRemoteReceiptLogs(t, repo, identity.DeploymentID, step, stepLabel)
}

func startRemoteReceiptClaim(
	t *testing.T, repo *repository.Repository, step bool,
	scrubber *logscrub.Scrubber,
) (repository.RemoteLifecycleClaim, string) {
	t.Helper()
	claim := currentClaimArg(t, repo)
	identity := remoteIdentity(claim)
	var stepLabel string
	if step {
		identity.DeploymentID = 2
		identity.ClaimTokenHash = append(
			[]byte(nil),
			identity.ClaimTokenHash...)
		identity.ClaimTokenHash[0]++
		// A valid source name fits NVARCHAR(255), but its raw agent suffix
		// does not. Start and publication must use the same scrubbed name.
		name := strings.Repeat(
			"a",
			150,
		) + " password=" + strings.Repeat(
			"b",
			95,
		)
		if _, err := repo.DB.ExecContext(t.Context(),
			"UPDATE deployment_steps SET name=? WHERE deployment_id=2 AND step_index=0", name); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.DB.ExecContext(t.Context(), `INSERT INTO remote_step_runs
			(deployment_id,step_index,agent_id,state,claim_token_hash,ciphertext,
			claim_expires_at,last_heartbeat_at,started_at,updated_at)
			VALUES (2,0,'a','claimed',?,'fixture',?,100,NULL,100)`, identity.ClaimTokenHash, claim.ClaimExpiresAt); err != nil {
			t.Fatal(err)
		}
		started, handled, err := repo.StartRemoteStep(
			t.Context(),
			identity,
			scrubber,
		)
		if err != nil || !handled || len(started) != 1 {
			t.Fatalf("long-name start: handled=%v error=%v", handled, err)
		}
		stepLabel = started[0].StepName.String
	} else {
		n, err := repo.ClaimRemoteDeployment(t.Context(), claim)
		assertOne(t, n, err)
		if err := repo.StartRemoteDeployment(t.Context(), identity); err != nil {
			t.Fatal(err)
		}
	}
	return identity, stepLabel
}

func readRemoteReceiptState(
	t *testing.T, repo *repository.Repository, box *secret.Box,
	query string, deploymentID int64,
) (int64, bool) {
	t.Helper()
	var encrypted string
	if err := repo.DB.QueryRowContext(t.Context(), query, deploymentID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := box.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Version         int
		ReceivedThrough int64 `json:"received_through"`
		Suppressed      bool
	}
	if err := json.Unmarshal([]byte(plain), &state); err != nil ||
		state.Version != 2 {
		t.Fatalf("state decode: %v", err)
	}
	return state.ReceivedThrough, state.Suppressed
}

func assertInitialRemoteReceipt(
	t *testing.T, appendLogs func([]repository.RemoteLogEvent) (int, error),
) {
	t.Helper()
	// PostgreSQL TEXT rejects NUL. Publication must escape it instead of
	// making acceptance depend on whether redaction retains this event.
	if n, err := appendLogs([]repository.RemoteLogEvent{{Sequence: 1, Line: "safe\x00\n"}}); err != nil ||
		n != 1 {
		t.Fatalf("initial receipt: %d %v", n, err)
	}
}

func assertRemoteReceiptCursor(
	t *testing.T, readState func() (int64, bool),
	wantCursor int64, wantSuppressed bool,
) {
	t.Helper()
	if cursor, suppressed := readState(); cursor != wantCursor ||
		suppressed != wantSuppressed {
		t.Fatalf("receipt cursor=%d suppressed=%v, want %d %v",
			cursor, suppressed, wantCursor, wantSuppressed)
	}
}

func assertActiveReceiptFlushRejected(
	t *testing.T, repo *repository.Repository,
	identity repository.RemoteLifecycleClaim, step bool,
	scrubber *logscrub.Scrubber,
) {
	t.Helper()
	if step {
		if _, _, err := repo.FlushRemoteStepLogs(t.Context(), identity, scrubber); err == nil {
			t.Fatal("active standalone flush accepted")
		}
	} else {
		if _, err := repo.FlushRemoteDeploymentLogs(t.Context(), identity, scrubber); err == nil {
			t.Fatal("active standalone flush accepted")
		}
	}
}

func fillRemoteReceiptTail(
	t *testing.T, appendLogs func([]repository.RemoteLogEvent) (int, error),
) {
	t.Helper()
	for sequence := int64(2); sequence <= agentproto.MaxLogEvents+2; sequence++ {
		line := ""
		if sequence == 2 {
			line = "hidden-"
		}
		if _, err := appendLogs([]repository.RemoteLogEvent{{Sequence: sequence, Line: line}}); err != nil {
			t.Fatal(err)
		}
	}
}

func finishRemoteReceiptClaim(
	t *testing.T, repo *repository.Repository,
	identity repository.RemoteLifecycleClaim, step bool,
	scrubber *logscrub.Scrubber,
) {
	t.Helper()
	if step {
		if _, _, err := repo.FinishRemoteStepWithLogs(t.Context(), identity, "succeeded", scrubber); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, _, err := repo.FinishRemoteDeploymentWithLogs(t.Context(), identity, "succeeded", scrubber); err != nil {
			t.Fatal(err)
		}
	}
}

func assertRemoteReceiptLogs(
	t *testing.T, repo *repository.Repository,
	deploymentID int64, step bool, stepLabel string,
) {
	t.Helper()
	logs, err := repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	foundEscapedNUL := false
	for _, log := range logs {
		if step && strings.HasSuffix(log.Line, ` agent_id="a"`) &&
			log.StepName.String != stepLabel {
			t.Fatal("published metadata differs from claim start")
		}
		foundEscapedNUL = foundEscapedNUL ||
			strings.Contains(log.Line, `safe\0\n`)
		if strings.ContainsRune(log.Line, '\x00') {
			t.Fatal("NUL reached a public log row")
		}
		if strings.Contains(log.Line, "hidden-") ||
			strings.Contains(log.Line, "end safe text") {
			t.Fatal("suppressed content escaped")
		}
	}
	if !foundEscapedNUL {
		t.Fatal("NUL event lost its escaped text")
	}
}
