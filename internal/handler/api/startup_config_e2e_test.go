//go:build e2e

package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/provenance"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestStartupConfigProvenanceIsolationAPIWebE2E(t *testing.T) {
	f := newStartupProvenanceFixture(t)
	t.Setenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS", "live-proof")
	apiPath := f.base() + "/releases"
	webPath := strings.TrimPrefix(apiPath, "/api/v1")
	f.api(t, "POST", apiPath, map[string]string{
		"version": "api-safe", "artifact_ref": "live-proof",
	}, 201)
	f.web(t, "POST", webPath, url.Values{
		"version": {"web-safe"}, "artifact_ref": {"live-proof"},
	}, 303)
	apiResult := f.api(t, "POST", apiPath, map[string]string{
		"version": "api-rejected", "artifact_ref": "configured-proof",
	}, 422)
	webResult := f.web(t, "POST", webPath, url.Values{
		"version": {"web-rejected"}, "artifact_ref": {"configured-proof"},
	}, 422)
	if strings.Contains(string(apiResult), "configured-proof") ||
		strings.Contains(webResult, "configured-proof") {
		t.Fatal("provenance rejection echoed the configured secret pattern")
	}
	if _, err := handler.CreateReleaseWithProvenance(
		t.Context(), f.h.repo, f.project.ID, "direct-rejected",
		provenance.Source{ArtifactRef: "configured-proof"},
		repository.IdempotencyRequest{},
	); err == nil {
		t.Fatal("shared release helper bypassed the captured pattern")
	}
	checkDuplicateReleaseCount(t, f, 2)
}

func newStartupProvenanceFixture(t *testing.T) *artifactE2E {
	t.Helper()
	conn, err := migrate.Run(filepath.Join(t.TempDir(), "startup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	settings, err := config.LoadWithLookup(func(key string) (string, bool) {
		switch key {
		case "DURPDEPLOY_EXTRA_SCRUB_PATTERNS":
			return "configured-proof", true
		case "DURPDEPLOY_EMBEDDED_AGENT_ENABLED":
			return "false", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.NewConfigured(conn, settings)
	rnr := runner.New(repo, runner.NewLogBroker())
	t.Cleanup(rnr.KillAll)
	h := &harness{repo: repo, runner: rnr}
	user := seedAPIUser(t, repo, "startup-provenance@test.local", "admin")
	_, token := seedAPIToken(t, repo, user.ID)
	srv := httptest.NewServer(server.NewRouter(repo, rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(repo)))
	t.Cleanup(srv.Close)
	return &artifactE2E{
		h: h, server: srv, baseURL: srv.URL, token: token,
		session: memberRoleSession(t, h, user.ID), csrf: "member-role-csrf",
		project: seedProject(t, repo),
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}
