package mfa_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
)

func TestWebAuthn_BaselineRepositoryAndRouterCompatibility(t *testing.T) {
	// Given: the untouched production repository and router dependencies.
	dsn := "file:" + filepath.Join(t.TempDir(), "mfa-baseline.db") +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate repository: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	repo := repository.New(conn)
	rnr := runner.New(repo, runner.NewLogBroker())
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	router := server.NewRouter(repo, rnr, parser, handler.NewAuthHandler(repo))

	// When: the public health endpoint is served through the constructed router.
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
	)

	// Then: router construction remains compatible with an untouched database.
	if response.Code != http.StatusOK {
		t.Fatalf(
			"health response status = %d, want %d",
			response.Code,
			http.StatusOK,
		)
	}
}
