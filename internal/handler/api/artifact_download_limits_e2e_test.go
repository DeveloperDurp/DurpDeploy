//go:build e2e

package api_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

func newArtifactDownloadFixture(t *testing.T, limit int64) *artifactE2E {
	t.Helper()
	settings := config.Defaults()
	settings.Container.Enabled = "false"
	settings.Limits.Artifact.DownloadBytes = limit
	f := newArtifactPolicyFixture(t, settings)
	setArtifactDownloadArchive(t, f, limit)
	settings.Limits.Artifact.DownloadBytes = 1
	t.Setenv("DURPDEPLOY_LIMITS_ARTIFACT_DOWNLOAD_BYTES", "1")
	return f
}

func newArtifactPolicyFixture(
	t *testing.T,
	settings config.Config,
) *artifactE2E {
	t.Helper()
	// Application settings require explicit loader presence, unlike typed limits.
	values := map[string]string{
		"DURPDEPLOY_EMBEDDED_AGENT_ENABLED": settings.Container.Enabled,
		"DURPDEPLOY_CONTAINER_RUNTIME":      settings.Container.Runtime,
		"DURPDEPLOY_CONTAINER_NAMESPACE":    settings.Container.Namespace,
	}
	resolved, err := config.LoadWithLookup(
		func(key string) (string, bool) { value, ok := values[key]; return value, ok },
	)
	require.NoError(t, err)
	resolved.Limits = settings.Limits
	settings = resolved
	require.NoError(t, settings.Limits.Validate())
	h := newConfiguredAPIHarness(t, &settings)
	t.Cleanup(h.runner.KillAll)
	if settings.Container.Enabled == "true" {
		t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", settings.Container.Namespace)
		cleanupArtifactRuntime(t, h)
	}
	box, err := secret.NewBox(make([]byte, 32))
	require.NoError(t, err)
	h.repo.SetSecretBox(box)
	user := seedAPIUser(t, h.repo, "download@example.test", "admin")
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
		h:             h,
		baseURL:       srv.URL,
		client:        srv.Client(),
		token:         token,
		session:       session,
		csrf:          csrf,
		project:       seedProject(t, h.repo),
		changePackage: change,
	}
	f.api(t, "PUT", f.base()+"/package-repository", map[string]string{
		"url_template": upstream + "/{version}.zip", "auth_type": "bearer",
		"credential": "artifact-secret",
	}, http.StatusOK)
	return f
}

func setArtifactDownloadArchive(t *testing.T, f *artifactE2E, limit int64) {
	t.Helper()
	// Keep a valid, small ZIP behind a prefix that crosses the lower budget.
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	entry, err := w.Create("app.txt")
	require.NoError(t, err)
	_, err = entry.Write([]byte("download policy acceptance"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	file := filepath.Join(t.TempDir(), "package.zip")
	archive, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0600)
	require.NoError(t, err)
	prefix := int64(1536)
	if limit > artifact.MaxDownload {
		prefix = artifact.MaxDownload + 1
	}
	_, err = archive.Seek(prefix, 0)
	require.NoError(t, err)
	_, err = archive.Write(data.Bytes())
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	f.changePackage("file:" + file)
}

func TestConfiguredArtifactDownloadAPIWebE2E(t *testing.T) {
	for _, limit := range []int64{1024, 2048, 2 * artifact.MaxDownload} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			f := newArtifactDownloadFixture(t, limit)
			want := http.StatusOK
			if limit == 1024 {
				want = http.StatusBadGateway
			}
			body := f.api(
				t,
				"POST",
				f.base()+"/package-repository/test",
				map[string]string{"version": "1"},
				want,
			)
			require.NotContains(t, string(body), "artifact-secret")
			page := f.web(
				t,
				"POST",
				fmt.Sprintf(
					"/projects/%d/package-repository/test",
					f.project.ID,
				),
				url.Values{"version": {"1"}},
				200,
			)
			if want != http.StatusOK {
				require.Contains(
					t,
					page,
					"Could not retrieve and validate this ZIP",
				)
			} else {
				require.Contains(t, page, "ZIP")
				require.NotContains(t, page, "Could not retrieve and validate this ZIP")
			}
			entries, err := os.ReadDir(f.h.repo.ArtifactClient.TempDir)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestConfiguredArtifactDownloadRemoteAPIE2E(t *testing.T) {
	f := newArtifactDownloadFixture(t, 2048)
	f.environment = seedEnv(t, f.h.repo)
	seedIncompatiblePolicyAgent(t, f)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Remote package policy", "script_body": "echo must-not-run",
		"execution_target": "agent", "agent_execution_mode": "host",
	}, 201)
	release := verificationRelease(t, f, "1")
	deployment := verificationDeploy(t, f, release)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	require.Eventually(t, func() bool {
		var current db.Deployment
		require.NoError(
			t,
			json.Unmarshal(f.api(t, "GET", path, nil, 200), &current),
		)
		return current.Status == "failed"
	}, 10*time.Second, 20*time.Millisecond)
	logs := f.api(t, "GET", path+"/logs", nil, 200)
	require.Contains(
		t,
		string(logs),
		"resource policy is incompatible",
	)
	var runs int
	require.NoError(
		t,
		f.h.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM remote_step_runs WHERE deployment_id = ?", deployment.ID).
			Scan(&runs),
	)
	require.Zero(t, runs)
}
