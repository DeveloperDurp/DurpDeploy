//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
)

func TestConfiguredArtifactTimeoutsAPIWebE2E(t *testing.T) {
	for _, mode := range []string{"slow-headers", "slow-body"} {
		for _, seconds := range []int64{1, 3} {
			t.Run(fmt.Sprintf("%s/%d", mode, seconds), func(t *testing.T) {
				checkArtifactTimeoutAPI(t, mode, seconds)
			})
		}
	}
}

func checkArtifactTimeoutAPI(t *testing.T, mode string, seconds int64) {
	t.Helper()
	settings := config.Defaults()
	settings.Container.Enabled = "false"
	settings.Limits.Artifact.DialTimeoutSeconds = 2
	settings.Limits.Artifact.TLSTimeoutSeconds = 2
	if mode == "slow-headers" {
		settings.Limits.Artifact.HeaderTimeoutSeconds = seconds
	} else {
		settings.Limits.Artifact.FetchTimeoutSeconds = seconds
	}
	h := newConfiguredAPIHarness(t, &settings)
	t.Cleanup(h.runner.KillAll)
	box, err := secret.NewBox(make([]byte, 32))
	require.NoError(t, err)
	h.repo.SetSecretBox(box)
	user := seedAPIUser(t, h.repo, "timeout@example.test", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	session, csrf, err := auth.NewSessionToken()
	require.NoError(t, err)
	_, err = h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: session, UserID: user.ID, CsrfToken: csrf,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	upstream, trusted, change, _ := artifactHTTPSFixture(t)
	transport := h.repo.ArtifactClient.HTTP.Transport.(*http.Transport)
	transport.TLSClientConfig = trusted.HTTP.Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(transport.CloseIdleConnections)
	h.repo.ArtifactClient.TempDir = t.TempDir()
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	f := &artifactE2E{
		h:       h,
		baseURL: srv.URL,
		client:  srv.Client(),
		token:   token,
		session: session,
		csrf:    csrf,
		project: seedProject(t, h.repo),
	}
	base := f.base()
	f.api(t, "PUT", base+"/package-repository", map[string]string{
		"url_template": upstream + "/{version}.zip", "auth_type": "bearer",
		"credential": "artifact-secret",
	}, http.StatusOK)
	// Later environment and caller mutations cannot alter this instance.
	t.Setenv("DURPDEPLOY_LIMITS_ARTIFACT_FETCH_TIMEOUT_SECONDS", "100")
	t.Setenv("DURPDEPLOY_LIMITS_ARTIFACT_HEADER_TIMEOUT_SECONDS", "100")
	settings.Limits.Artifact = config.Defaults().Limits.Artifact
	change(mode)
	want := http.StatusOK
	if seconds == 1 {
		want = http.StatusBadGateway
	}
	response := f.api(t, "POST", base+"/package-repository/test",
		map[string]string{"version": "1"}, want)
	if want == http.StatusBadGateway {
		require.NotContains(t, string(response), "artifact-secret")
	} else {
		var pin struct {
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		}
		require.NoError(t, json.Unmarshal(response, &pin))
		require.Positive(t, pin.Size)
		require.Len(t, pin.SHA256, 64)
	}
	page := f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/package-repository/test", f.project.ID),
		url.Values{"version": {"1"}},
		http.StatusOK,
	)
	if want == http.StatusBadGateway {
		require.Contains(t, page, "Could not retrieve and validate this ZIP")
	} else {
		require.NotContains(t, page, "Could not retrieve and validate this ZIP")
	}
	change("package")
	f.api(
		t,
		"POST",
		base+"/package-repository/test",
		map[string]string{"version": "1"},
		http.StatusOK,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/package-repository/test", f.project.ID),
		url.Values{"version": {"1"}},
		http.StatusOK,
	)
	entries, err := os.ReadDir(h.repo.ArtifactClient.TempDir)
	require.NoError(t, err)
	require.Empty(t, entries)
	// The configured client retains the destination filter.
	_, err = h.repo.ArtifactClient.Fetch(t.Context(), artifact.Repository{
		URLTemplate: "https://127.0.0.1/{version}.zip", AuthType: "noauth",
	}, artifact.Pin{URL: "https://127.0.0.1/1.zip"})
	require.ErrorIs(t, err, artifact.ErrFetch)
}
