package dispatch

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestMaintainRequeuesExpiredUnstartedClaim(t *testing.T) {
	repo := lifecycleFixture(t, "claimed", 100, sql.NullInt64{})
	if err := New(repo).Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	claim, err := repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != "waiting" || claim.ClaimTokenHash != nil ||
		claim.Ciphertext.Valid || claim.ClaimExpiresAt.Valid {
		t.Fatalf("requeued claim=%+v error=%v", claim, err)
	}
}

func TestMaintainRequeuesExpiredUnstartedStepClaim(t *testing.T) {
	repo := lifecycleFixture(t, "claimed", 4102444800, sql.NullInt64{})
	hash := sha256.Sum256([]byte("step-claim"))
	for _, statement := range []string{
		`DELETE FROM remote_deployment_claims WHERE deployment_id=1`,
		`UPDATE deployments SET assigned_agent_id=NULL WHERE id=1`,
		`INSERT INTO deployment_steps
		 (deployment_id,step_index,name,script_body,execution_target)
		 VALUES(1,0,'remote','echo remote','agent')`,
		`INSERT INTO remote_step_runs
		 (deployment_id,step_index,agent_id,state,claim_token_hash,
		  ciphertext,claim_expires_at,last_heartbeat_at,updated_at)
		 VALUES(1,0,'a','claimed',x'` + fmt.Sprintf("%x", hash) +
			`','ciphertext',100,100,100)`,
	} {
		if _, err := repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := New(repo).Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	var state string
	var token, ciphertext, expiresAt sql.NullString
	if err := repo.DB.QueryRow(`SELECT state,claim_token_hash,ciphertext,
		claim_expires_at FROM remote_step_runs
		WHERE deployment_id=1 AND step_index=0 AND agent_id='a'`).Scan(
		&state, &token, &ciphertext, &expiresAt,
	); err != nil {
		t.Fatal(err)
	}
	if state != "waiting" || token.Valid || ciphertext.Valid ||
		expiresAt.Valid {
		t.Fatalf(
			"expired remote step claim not requeued: state=%q token=%v ciphertext=%v expires=%v",
			state,
			token.Valid,
			ciphertext.Valid,
			expiresAt.Valid,
		)
	}
}

func TestMaintainFailsExpiredStartedClaim(t *testing.T) {
	repo := lifecycleFixture(t, "started", 200,
		sql.NullInt64{Int64: 100, Valid: true})
	setLegacyLogBuffer(t, repo)
	if err := New(repo).Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertFailedLifecycle(t, repo, "lost", "remote_agent_lost")
	assertLegacyLogBufferCleared(t, repo)
}

func TestRemoteCancellationTimeout(t *testing.T) {
	repo := lifecycleFixture(t, "cancel_requested", 200,
		sql.NullInt64{Int64: 100, Valid: true})
	if _, err := repo.DB.Exec(`UPDATE remote_deployment_claims
		SET cancel_requested_at=100, log_buffer_ciphertext=zeroblob(16)
		WHERE deployment_id=1`); err != nil {
		t.Fatal(err)
	}
	if err := New(repo).Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertFailedLifecycle(t, repo, "cancel_unconfirmed",
		"remote_cancel_unconfirmed")
	assertLegacyLogBufferCleared(t, repo)
}

func TestMaintainMarksStaleRemoteStepCancellationUnconfirmed(t *testing.T) {
	repo := lifecycleFixture(t, "claimed", 4102444800, sql.NullInt64{})
	hash := sha256.Sum256([]byte("step-claim"))
	for _, statement := range []string{
		`DELETE FROM remote_deployment_claims WHERE deployment_id=1`,
		`UPDATE deployments SET assigned_agent_id=NULL WHERE id=1`,
		`INSERT INTO deployment_steps
		 (deployment_id,step_index,name,script_body,execution_target)
		 VALUES(1,0,'remote','echo remote','agent')`,
		`INSERT INTO remote_step_runs
		 (deployment_id,step_index,agent_id,state,claim_token_hash,
		  ciphertext,claim_expires_at,last_heartbeat_at,started_at,
		  cancel_requested_at,updated_at,log_buffer_ciphertext)
		 VALUES(1,0,'a','cancel_requested',x'` + fmt.Sprintf("%x", hash) +
			`','ciphertext',200,100,100,100,100,zeroblob(16))`,
	} {
		if _, err := repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := New(repo).Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	var state string
	var finishedAt sql.NullInt64
	var logBuffer []byte
	if err := repo.DB.QueryRow(`SELECT state,finished_at,log_buffer_ciphertext
		FROM remote_step_runs WHERE deployment_id=1 AND step_index=0`,
	).Scan(&state, &finishedAt, &logBuffer); err != nil {
		t.Fatal(err)
	}
	if state != "cancel_unconfirmed" || !finishedAt.Valid || logBuffer != nil {
		t.Fatalf(
			"stale remote step state=%q finished=%v log_buffer=%x",
			state, finishedAt, logBuffer,
		)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "failed" ||
		!deployment.FinishedAt.Valid {
		t.Fatalf("failed deployment=%+v error=%v", deployment, err)
	}
}

func TestMaintainLeavesAcknowledgedCancellationForRunner(t *testing.T) {
	repo := lifecycleFixture(t, "claimed", 4102444800, sql.NullInt64{})
	for _, statement := range []string{
		`DELETE FROM remote_deployment_claims WHERE deployment_id=1`,
		`UPDATE deployments SET assigned_agent_id=NULL WHERE id=1`,
		`INSERT INTO deployment_steps
		 (deployment_id,step_index,name,script_body,execution_target)
		 VALUES(1,0,'remote','echo remote','agent')`,
		`INSERT INTO remote_step_runs
		 (deployment_id,step_index,agent_id,state,started_at,finished_at,
		  cancel_requested_at,updated_at)
		 VALUES(1,0,'a','cancelled',100,110,105,110)`,
	} {
		if _, err := repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := New(repo).Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "running" ||
		deployment.FinishedAt.Valid {
		t.Fatalf("deployment=%+v error=%v", deployment, err)
	}
}

func TestMaintainMarksRemoteStepLostWhenHeartbeatStale(t *testing.T) {
	repo := lifecycleFixture(t, "claimed", 4102444800, sql.NullInt64{})
	hash := sha256.Sum256([]byte("step-claim"))
	for _, statement := range []string{
		`DELETE FROM remote_deployment_claims WHERE deployment_id=1`,
		`UPDATE deployments SET assigned_agent_id=NULL WHERE id=1`,
		`INSERT INTO deployment_steps
		 (deployment_id,step_index,name,script_body,execution_target)
		 VALUES(1,0,'remote','echo remote','agent')`,
		`INSERT INTO remote_step_runs
		 (deployment_id,step_index,agent_id,state,claim_token_hash,
		  ciphertext,claim_expires_at,last_heartbeat_at,started_at,updated_at,
		  log_buffer_ciphertext)
		 VALUES(1,0,'a','started',x'` + fmt.Sprintf("%x", hash) +
			`','ciphertext',200,100,100,100,zeroblob(16))`,
	} {
		if _, err := repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := New(repo).Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	var state string
	var finishedAt sql.NullInt64
	var logBuffer []byte
	if err := repo.DB.QueryRow(`SELECT state,finished_at,log_buffer_ciphertext
		FROM remote_step_runs WHERE deployment_id=1 AND step_index=0`,
	).Scan(&state, &finishedAt, &logBuffer); err != nil {
		t.Fatal(err)
	}
	if state != "lost" || !finishedAt.Valid || logBuffer != nil {
		t.Fatalf(
			"stale remote step state=%q finished=%v log_buffer=%x",
			state, finishedAt, logBuffer,
		)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "failed" ||
		!deployment.FinishedAt.Valid {
		t.Fatalf("failed deployment=%+v error=%v", deployment, err)
	}
}

func setLegacyLogBuffer(t *testing.T, repo *repository.Repository) {
	t.Helper()
	if _, err := repo.DB.Exec(`UPDATE remote_deployment_claims
		SET log_buffer_ciphertext=zeroblob(16) WHERE deployment_id=1`); err != nil {
		t.Fatal(err)
	}
}

func assertLegacyLogBufferCleared(t *testing.T, repo *repository.Repository) {
	t.Helper()
	var logBuffer []byte
	if err := repo.DB.QueryRow(`SELECT log_buffer_ciphertext
		FROM remote_deployment_claims WHERE deployment_id=1`).Scan(
		&logBuffer,
	); err != nil {
		t.Fatal(err)
	}
	if logBuffer != nil {
		t.Fatalf("terminal maintenance retained log buffer %x", logBuffer)
	}
}

func TestMaintainWaitsForEveryRemoteStepRunToFinish(t *testing.T) {
	repo := lifecycleFixture(t, "claimed", 4102444800, sql.NullInt64{})
	hash := sha256.Sum256([]byte("step-claim"))
	for _, statement := range []string{
		`DELETE FROM remote_deployment_claims WHERE deployment_id=1`,
		`UPDATE deployments SET assigned_agent_id=NULL WHERE id=1`,
		`INSERT INTO agents(id,name,endpoint,status)
		 VALUES('b','b','https://agent-b','pending')`,
		`INSERT INTO deployment_steps
		 (deployment_id,step_index,name,script_body,execution_target)
		 VALUES(1,0,'remote','echo remote','agent')`,
		`INSERT INTO remote_step_runs
		 (deployment_id,step_index,agent_id,state,claim_token_hash,
		  ciphertext,claim_expires_at,last_heartbeat_at,started_at,updated_at)
		 VALUES(1,0,'a','started',x'` + fmt.Sprintf("%x", hash) +
			`','ciphertext',200,100,100,100)`,
		`INSERT INTO remote_step_runs
		 (deployment_id,step_index,agent_id,state,updated_at)
		 VALUES(1,0,'b','waiting',100)`,
	} {
		if _, err := repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := New(repo).Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "running" {
		t.Fatalf("deployment=%+v error=%v", deployment, err)
	}
}

func lifecycleFixture(
	t *testing.T,
	state string,
	expiresAt int64,
	startedAt sql.NullInt64,
) *repository.Repository {
	t.Helper()
	conn, err := migrate.Run("file:" + t.TempDir() +
		"/lifecycle.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	repo := repository.New(conn)
	hash := sha256.Sum256([]byte("claim"))
	statements := []string{
		`INSERT INTO projects(id,name) VALUES(1,'p')`,
		`INSERT INTO environments(id,name) VALUES(1,'e')`,
		`INSERT INTO releases(id,project_id,version,steps_json)
		 VALUES(1,1,'v1','[]')`,
		`INSERT INTO agents(id,name,endpoint,status,certificate_pem,
		 certificate_fingerprint,encrypted_identity)
		 VALUES('a','a','https://agent','active','cert','` + strings.Repeat("a", 64) + `','cipher')`,
		`INSERT INTO agent_pairings(agent_id,pairing_code_hash,
		 agent_public_identity,agent_pin,server_public_identity,server_pin,
		 encrypted_identity,state,expires_at,paired_at)
		 VALUES('a',zeroblob(32),'agent','` + strings.Repeat("b", 64) +
			`','server','` + strings.Repeat("c", 64) + `',
		 'cipher','paired',9999999999,1)`,
		`INSERT INTO deployments(id,release_id,environment_id,status,
		 assigned_agent_id) VALUES(1,1,1,'running','a')`,
	}
	for _, statement := range statements {
		if _, err := repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	_, err = repo.DB.Exec(`INSERT INTO remote_deployment_claims
		(deployment_id,agent_id,state,claim_token_hash,ciphertext,
		 claim_expires_at,last_heartbeat_at,started_at,cancel_requested_at,
		 updated_at) VALUES(1,'a',?,?,?,?,?,?,?,100)`, state, hash[:],
		"ciphertext", expiresAt, int64(100), startedAt,
		func() sql.NullInt64 {
			if state == "cancel_requested" {
				return sql.NullInt64{Int64: 100, Valid: true}
			}
			return sql.NullInt64{}
		}())
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func assertFailedLifecycle(
	t *testing.T,
	repo *repository.Repository,
	state string,
	reason string,
) {
	t.Helper()
	claim, err := repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != state || claim.Reason.String != reason {
		t.Fatalf("terminal claim=%+v error=%v", claim, err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "failed" ||
		!deployment.FinishedAt.Valid {
		t.Fatalf("failed deployment=%+v error=%v", deployment, err)
	}
}
