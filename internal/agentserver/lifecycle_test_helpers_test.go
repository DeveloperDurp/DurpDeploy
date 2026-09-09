package agentserver_test

import (
	"crypto/sha256"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func lifecycleDeploymentID(path string) string {
	if path == agentproto.CancelledPath {
		return "2"
	}
	return "1"
}

func seedAgentLifecycle(t *testing.T, repo *repository.Repository) {
	t.Helper()
	expiresAt := int64(2_000_000_000)
	lastHeartbeat := int64(1)
	firstHash := sha256.Sum256([]byte("claim-one"))
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO projects (id,name) VALUES (1,'agent-boundary')`, nil},
		{`INSERT INTO environments (id,name) VALUES (1,'agent-boundary')`, nil},
		{`INSERT INTO releases (id,project_id,version,steps_json)
			VALUES (1,1,'agent-boundary','[]')`, nil},
		{`INSERT INTO deployments
			(id,release_id,environment_id,status,assigned_agent_id)
			VALUES (1,1,1,'pending','test-agent'),
			       (2,1,1,'running','test-agent')`, nil},
		{`INSERT INTO remote_deployment_claims
			(deployment_id,agent_id,state,claim_token_hash,ciphertext,
			 claim_expires_at,last_heartbeat_at,started_at,
			 cancel_requested_at,updated_at)
			VALUES (1,'test-agent','claimed',?,'ciphertext',?,?,NULL,NULL,?),
			       (2,'test-agent','waiting',NULL,NULL,NULL,NULL,NULL,NULL,?)`,
			[]any{
				firstHash[:], expiresAt, lastHeartbeat, lastHeartbeat,
				lastHeartbeat,
			}},
	}
	for _, statement := range statements {
		if _, err := repo.DB.Exec(
			statement.query,
			statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func activateCancellationFixture(t *testing.T, repo *repository.Repository) {
	t.Helper()
	expiresAt := int64(2_000_000_000)
	lastHeartbeat := int64(1)
	hash := sha256.Sum256([]byte("claim-two"))
	_, err := repo.DB.Exec(`UPDATE remote_deployment_claims
		SET state='cancel_requested', claim_token_hash=?, ciphertext='ciphertext',
			claim_expires_at=?, last_heartbeat_at=?, started_at=?,
			cancel_requested_at=?, updated_at=?
		WHERE deployment_id=2`, hash[:], expiresAt, lastHeartbeat,
		lastHeartbeat, lastHeartbeat, lastHeartbeat)
	if err != nil {
		t.Fatal(err)
	}
}

func agentState(
	t *testing.T,
	repo *repository.Repository,
) (sql.NullInt64, sql.NullString) {
	t.Helper()
	var heartbeat sql.NullInt64
	var version sql.NullString
	if err := repo.DB.QueryRow(
		"SELECT last_heartbeat_at, agent_version FROM agents WHERE id='test-agent'",
	).Scan(&heartbeat, &version); err != nil {
		t.Fatal(err)
	}
	return heartbeat, version
}

func postAgent(
	t *testing.T,
	fixture agentFixture,
	path string,
	body string,
) *http.Response {
	t.Helper()
	response, err := fixture.client.Post(
		fixture.server.URL+path,
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		response.Body.Close()
		t.Fatalf("POST %s response is cacheable", path)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}
