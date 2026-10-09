package agentserver_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/mssqldriver"
	"durpdeploy/internal/testutil"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestSQLServerPollRecoversRolledBackClaimThroughAPI(t *testing.T) {
	dsn := testutil.SQLServerDSN(t)
	f := newAgentFixtureWithDSN(t, dsn, nil)
	f.client.Timeout = 30 * time.Second
	id := seedPollPayload(t, f, "pending", "test-agent")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, err := f.repo.DB.ExecContext(ctx, `CREATE TABLE api_poll_locks (
id INT PRIMARY KEY, attempts INT NOT NULL);
INSERT INTO api_poll_locks VALUES (1,0),(2,0);`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.ExecContext(ctx, `CREATE TRIGGER api_poll_deadlock
ON remote_deployment_claims AFTER UPDATE AS
BEGIN
  IF EXISTS (SELECT 1 FROM inserted WHERE state='claimed')
  BEGIN
    UPDATE api_poll_locks SET attempts=attempts+1 WHERE id=1;
    UPDATE api_poll_locks SET attempts=attempts+1 WHERE id=2;
  END
END;`); err != nil {
		t.Fatal(err)
	}
	locker, err := sql.Open(mssqldriver.DriverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	tx, err := locker.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET DEADLOCK_PRIORITY HIGH;
UPDATE api_poll_locks SET attempts=attempts WHERE id=2;`); err != nil {
		t.Fatal(err)
	}
	var spid int
	if err := tx.QueryRowContext(ctx, "SELECT @@SPID").Scan(&spid); err != nil {
		t.Fatal(err)
	}
	done := make(chan pollResult, 1)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.server.URL+agentproto.PollPath, strings.NewReader(pollBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	go func() {
		response, err := f.client.Do(request)
		done <- pollResult{response: response, err: err}
	}()
	for {
		var waiting int
		if err := f.repo.DB.QueryRowContext(ctx, `SELECT COUNT(*)
FROM sys.dm_exec_requests WHERE blocking_session_id=?
AND database_id=DB_ID() AND wait_type LIKE 'LCK_M%'`, spid).
			Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case result := <-done:
			if result.response != nil {
				result.response.Body.Close()
			}
			t.Fatalf("API poll did not enter deadlock: %v", result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE api_poll_locks SET attempts=attempts WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer result.response.Body.Close()
	if result.response.StatusCode != http.StatusOK {
		t.Fatalf(
			"API deadlock recovery status=%d, want 200",
			result.response.StatusCode,
		)
	}
	if result.response.Header.Get("Content-Type") != "application/json" {
		t.Fatal("recovered poll did not return JSON")
	}
	poll := decodePollResponse(t, result.response)
	if int64(poll.DeploymentID) != id || poll.Payload == "" ||
		poll.ClaimToken == "" {
		t.Fatal("recovered poll did not return its sealed claim")
	}
	claim, err := f.repo.Queries.GetRemoteDeploymentClaim(ctx, id)
	if err != nil || claim.State != "claimed" ||
		claim.Ciphertext.String != poll.Payload {
		t.Fatalf("persisted claim state=%s error=%v", claim.State, err)
	}
	var attempts int
	if err := f.repo.DB.QueryRowContext(
		ctx,
		"SELECT SUM(attempts) FROM api_poll_locks",
	).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("failed API claim left partial writes: attempts=%d", attempts)
	}
	t.Logf(
		"API poll status=200 content-type=application/json deployment=%d sealed=true; committed writes=%d",
		id,
		attempts,
	)
}

func TestSQLServerConcurrentPollCapacityThroughAPI(t *testing.T) {
	f := newAgentFixtureWithDSN(t, testutil.SQLServerDSN(t), nil)
	// The poll that gets no work must complete its normal long-poll interval.
	f.client.Timeout = agentproto.PollInterval + 10*time.Second
	for range 2 {
		seedPollPayload(t, f, "pending", "test-agent")
	}
	start := make(chan struct{})
	done := make(chan concurrentPollResult, 2)
	for range 2 {
		go func() {
			<-start
			response, err := f.client.Post(f.server.URL+agentproto.PollPath,
				"application/json", strings.NewReader(pollBody))
			result := concurrentPollResult{err: err}
			if response != nil {
				result.status = response.StatusCode
				response.Body.Close()
			}
			done <- result
		}()
	}
	close(start)
	claims := 0
	for range 2 {
		result := <-done
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch result.status {
		case http.StatusOK:
			claims++
		case http.StatusNoContent:
		default:
			t.Fatalf("concurrent API poll status=%d", result.status)
		}
	}
	var persisted int
	if err := f.repo.DB.QueryRowContext(t.Context(), `SELECT COUNT(*)
FROM remote_deployment_claims WHERE state='claimed'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if claims != 1 || persisted != 1 {
		t.Fatalf(
			"API claims=%d persisted=%d, want exactly one",
			claims,
			persisted,
		)
	}
	t.Log(
		"Two concurrent authenticated API polls: 200 + 204, one persisted claim",
	)
}
