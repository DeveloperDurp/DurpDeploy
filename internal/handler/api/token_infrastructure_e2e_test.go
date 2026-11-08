//go:build e2e

package api_test

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"

	"github.com/robfig/cron/v3"
)

func TestTokenInfrastructureAPIE2E(t *testing.T) {
	// DELETE journal mode makes an exclusive writer block token reads.
	path := filepath.Join(t.TempDir(), "contention.db")
	conn, err := migrate.Run("file:" + path +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, conn) })
	repo := repository.New(conn)
	user := seedAPIUser(t, repo, "infrastructure@example.test", "admin")
	_, token := seedAPIToken(t, repo, user.ID)
	// Keep best-effort usage writes out of this authentication-read fixture.
	if _, err := conn.ExecContext(t.Context(),
		"UPDATE api_tokens SET last_used_at = ?", time.Now().Unix(),
	); err != nil {
		t.Fatal(err)
	}
	rnr := runner.New(repo, runner.NewLogBroker())
	srv := httptest.NewServer(server.NewRouter(repo, rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(repo)))
	t.Cleanup(srv.Close)
	f := &artifactE2E{baseURL: srv.URL, client: srv.Client(), token: token}
	tokenFormatResponse(t, f, token, "/api/v1/users/me", 200, "")
	writer, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, writer) })
	writer.SetMaxOpenConns(1)
	if _, err := writer.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = writer.Exec("ROLLBACK")
	})
	for _, route := range []string{
		"/api/v1/users/me", "/api/v1/projects",
		"/api/v1/deployments/1/logs/stream",
	} {
		tokenFormatResponse(t, f, token, route, 503,
			"internal server error")
	}
	if _, err := writer.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	tokenFormatResponse(t, f, token, "/api/v1/users/me", 200, "")
}
