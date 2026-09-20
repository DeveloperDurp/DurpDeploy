package agentserver_test

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type pollResult struct {
	response *http.Response
	err      error
}

func TestPollRecoversWhenHeartbeatSQLiteBusyClears(t *testing.T) {
	requestEntered := make(chan struct{})
	var enteredOnce sync.Once
	fixture, locker := newLockedPollFixture(t, func(r *http.Request) {
		if r.URL.Path == agentproto.PollPath {
			enteredOnce.Do(func() { close(requestEntered) })
		}
	})
	seedPollPayload(t, fixture, "pending", "test-agent")
	lock := holdPollWriter(t, locker)
	result := make(chan pollResult, 1)
	go func() {
		response, err := fixture.client.Post(
			fixture.server.URL+agentproto.PollPath,
			"application/json",
			strings.NewReader(pollBody),
		)
		result <- pollResult{response: response, err: err}
	}()

	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("poll did not enter the agent handler")
	}
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.response.Body.Close()
	if got.response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want=%d", got.response.StatusCode, http.StatusOK)
	}
}

func TestPollReturnsRetryableStatusWhenSQLiteBusyExhausted(t *testing.T) {
	fixture, locker := newLockedPollFixture(t, nil)
	deploymentID := seedPollPayload(t, fixture, "pending", "test-agent")
	lock := holdPollWriter(t, locker)

	response := postAgent(t, fixture, agentproto.PollPath, pollBody)
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf(
			"status=%d want=%d",
			response.StatusCode,
			http.StatusServiceUnavailable,
		)
	}
	if retryAfter := response.Header.Get("Retry-After"); retryAfter != "1" {
		t.Fatalf("Retry-After=%q want=1", retryAfter)
	}
	assertWaitingWithoutPayload(t, fixture, deploymentID)
}

func newLockedPollFixture(
	t *testing.T,
	requestStarted func(*http.Request),
) (agentFixture, *sql.DB) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "agent-poll.db") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(0)" +
		"&_pragma=journal_mode(WAL)"
	fixture := newAgentFixtureWithDSN(t, dsn, requestStarted)
	locker, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := locker.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := locker.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture, locker
}

func holdPollWriter(t *testing.T, locker *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := locker.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(
		t.Context(),
		"UPDATE agents SET updated_at=updated_at WHERE id='test-agent'",
	); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	return tx
}
