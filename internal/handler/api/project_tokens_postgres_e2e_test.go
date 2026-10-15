//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"

	"github.com/robfig/cron/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestProjectTokensPostgresSerializationAPIE2E(t *testing.T) {
	container, err := postgres.Run(t.Context(), "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies())
	testutil.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := container.ConnectionString(t.Context(), "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, conn) })
	repo := repository.New(conn)
	user := seedAPIUser(t, repo, "scope-retry@example.test", "admin")
	_, owner := seedAPIToken(t, repo, user.ID)
	rnr := runner.New(repo, runner.NewLogBroker())
	srv := httptest.NewServer(server.NewRouter(repo, rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Dow),
		handler.NewAuthHandler(repo)))
	t.Cleanup(srv.Close)
	f := &artifactE2E{baseURL: srv.URL, client: srv.Client(), token: owner}
	first, second := seedProject(t, repo), seedProject(t, repo)
	var token projectToken
	if err := json.Unmarshal(f.api(t, "POST", "/api/v1/tokens",
		map[string]any{"name": "retry", "scope": "scoped", "role": "viewer",
			"project_ids": []int64{first.ID}}, 201), &token); err != nil {
		t.Fatal(err)
	}
	// A sequence survives rollback, so only the first complete transaction fails.
	_, err = conn.ExecContext(t.Context(), `
CREATE SEQUENCE scope_retry_attempt;
CREATE FUNCTION scope_retry_conflict() RETURNS TRIGGER AS $$
BEGIN
    IF nextval('scope_retry_attempt') = 1 THEN
        RAISE EXCEPTION 'scope retry test' USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER scope_retry BEFORE UPDATE OF scope, token_role ON api_tokens
FOR EACH ROW EXECUTE FUNCTION scope_retry_conflict();`)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/tokens/" + token.TokenID
	grant := map[string]any{"scope": "scoped", "role": "deployer",
		"project_ids": []int64{second.ID}}
	f.api(t, "PATCH", path, grant, 200)
	var attempts int
	if err := conn.QueryRowContext(t.Context(),
		"SELECT last_value FROM scope_retry_attempt").Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("serialization retry attempts=%d error=%v", attempts, err)
	}
	var details string
	if err := conn.QueryRowContext(t.Context(),
		"SELECT details FROM audit_log WHERE action = 'update_api_token_scope'",
	).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var change struct {
		Previous repository.TokenScope `json:"previous_scope"`
	}
	if err := json.Unmarshal([]byte(details), &change); err != nil {
		t.Fatal(err)
	}
	if change.Previous.Role != "viewer" ||
		len(change.Previous.ProjectIDs) != 1 ||
		change.Previous.ProjectIDs[0] != first.ID {
		t.Fatal("serialization retry lost the committed previous grants")
	}
	for _, failure := range []struct {
		code  string
		tries int
	}{{"40001", 3}, {"23505", 1}} {
		_, err = conn.ExecContext(t.Context(), fmt.Sprintf(`
CREATE OR REPLACE FUNCTION scope_retry_conflict() RETURNS TRIGGER AS $$
BEGIN
    PERFORM nextval('scope_retry_attempt');
    RAISE EXCEPTION 'scope retry test' USING ERRCODE = '%s';
END;
$$ LANGUAGE plpgsql;`, failure.code))
		if err != nil {
			t.Fatal(err)
		}
		f.api(t, "PATCH", path, map[string]string{
			"scope": "global", "role": "admin",
		}, 500)
		attempts += failure.tries
		var actual int
		if err := conn.QueryRowContext(t.Context(),
			"SELECT last_value FROM scope_retry_attempt").Scan(&actual); err != nil || actual != attempts {
			t.Fatalf("SQLSTATE %s attempts=%d want=%d error=%v",
				failure.code, actual, attempts, err)
		}
	}
	var auditCount int
	if err := conn.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM audit_log WHERE action = 'update_api_token_scope'",
	).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf(
			"failed edits produced audit events: count=%d error=%v",
			auditCount,
			err,
		)
	}
	f.token = token.ID
	f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", first.ID), nil, 403)
	f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", second.ID), nil, 200)
}
