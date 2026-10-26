package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
)

func TestResourceConfigAPIE2E(t *testing.T) {
	for _, custom := range []bool{false, true} {
		name := "defaults"
		if custom {
			name = "configured"
		}
		t.Run(name, func(t *testing.T) { checkResourceConfigAPI(t, custom) })
	}
}

func checkResourceConfigAPI(t *testing.T, custom bool) {
	t.Helper()
	settings := config.Defaults()
	settings.Container.Enabled = "false"
	settings.SecretKey = "private-secret-key"
	settings.DB = "private-database-path"
	settings.SMTP.Pass = "private-smtp-password"
	settings.OIDC.ClientSecret = "private-oidc-secret"
	if custom {
		settings.Limits.Container.PIDs = 160
		settings.Limits.Container.HelperPIDs = 8
		settings.Limits.Container.HelperCPUs = 0.5
		settings.Limits.HTTP.JSONBytes = 1024
		settings.Limits.TokenTelemetry.IntervalSeconds = 30
	}
	require.NoError(t, settings.Limits.Validate())
	h := newConfiguredAPIHarness(t, &settings)
	t.Cleanup(h.runner.KillAll)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	// Responses must remain bound to this instance after environment changes.
	t.Setenv("DURPDEPLOY_LIMITS_CONTAINER_PIDS", "999")
	t.Setenv("DURPDEPLOY_SECRET_KEY", "private-live-secret")
	for _, role := range []string{
		"anonymous", "invalid", "viewer", "deployer", "maintainer", "admin",
		"scoped-admin", "ceiling-viewer",
	} {
		t.Run(role, func(t *testing.T) {
			authorization := resourceConfigAuthorization(t, h, role)
			request, err := http.NewRequest(http.MethodGet,
				srv.URL+"/api/v1/admin/resource-config", nil)
			require.NoError(t, err)
			request.Header.Set("Authorization", authorization)
			response, err := srv.Client().Do(request)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, response.Body.Close())
			require.NoError(t, err)
			want := http.StatusForbidden
			switch role {
			case "anonymous", "invalid":
				want = http.StatusUnauthorized
			case "admin":
				want = http.StatusOK
			}
			require.Equal(t, want, response.StatusCode)
			require.NotContains(t, string(body), "private-")
			if role == "admin" {
				wantBody, err := json.Marshal(settings.Limits)
				require.NoError(t, err)
				require.JSONEq(t, string(wantBody), string(body))
				require.Equal(
					t,
					"no-store",
					response.Header.Get("Cache-Control"),
				)
				require.Contains(
					t,
					response.Header.Get("Content-Type"),
					"application/json",
				)
			}
		})
	}
}

func resourceConfigAuthorization(t *testing.T, h *harness, role string) string {
	t.Helper()
	if role == "anonymous" {
		return ""
	}
	if role == "invalid" {
		return "Bearer invalid"
	}
	ownerRole := role
	if role == "scoped-admin" || role == "ceiling-viewer" {
		ownerRole = "admin"
	}
	user := seedAPIUser(t, h.repo, role+"@resource-config.test", ownerRole)
	id, token := seedAPIToken(t, h.repo, user.ID)
	if role == "scoped-admin" || role == "ceiling-viewer" {
		grant := repository.TokenScope{Scope: "global", Role: "viewer"}
		if role == "scoped-admin" {
			grant = repository.TokenScope{Scope: "scoped", Role: "admin",
				ProjectIDs: []int64{seedProject(t, h.repo).ID}}
		}
		_, err := h.repo.ReplaceTokenScope(t.Context(), id, grant)
		require.NoError(t, err)
	}
	return "Bearer " + token
}
