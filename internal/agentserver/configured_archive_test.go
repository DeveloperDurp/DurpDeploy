package agentserver_test

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/config"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/stretchr/testify/require"
)

func TestConfiguredStagingArchiveHTTP(t *testing.T) {
	for _, mode := range []string{"declared", "chunked", "canonical", "exact", "download"} {
		t.Run(mode, func(t *testing.T) {
			f, id, token := startedStagingAdmission(t)
			settings := config.Defaults()
			settings.Limits.Staging = config.StagingLimits{
				PayloadBytes: 4,
				Entries:      3,
				ArchiveBytes: 2048,
			}
			if mode == "download" {
				settings.Limits.Staging.ArchiveBytes--
			}
			require.NoError(t, settings.Limits.Validate())
			// Recreate the listener with the persisted, already-started claim.
			repo := repository.NewConfigured(f.repo.DB, settings)
			box, err := secret.NewBox(make([]byte, 32))
			require.NoError(t, err)
			repo.SetSecretBox(box)
			agents, err := agentserver.New(agentserver.Config{
				Repository: repo, Dispatcher: dispatch.New(repo), Identity: f.serverIdentity,
				Broker: f.broker, EventBus: f.bus,
			})
			require.NoError(t, err)
			listener := httptest.NewUnstartedServer(
				server.NewAgentRouter(agents),
			)
			listener.TLS = f.server.TLS.Clone()
			listener.StartTLS()
			t.Cleanup(listener.Close)
			f.server = listener
			if mode == "download" {
				stageRequest(t, f, id, token, "download", nil, 422)
				return
			}
			name, body, want := "file", "data", 204
			switch mode {
			case "declared", "chunked":
				body, want = strings.Repeat("x", 1025), 413
			case "canonical":
				name, want = "a/b/file", 422
			}
			data := stageArchive(t, map[string]string{name: body})
			r := f.stagingAdmissionRequest(
				t,
				t.Context(),
				id,
				token,
				"upload",
				"",
				data,
			)
			if mode == "chunked" {
				r.Body, r.ContentLength, r.GetBody = io.NopCloser(
					bytes.NewReader(data),
				), -1, nil
			}
			response, err := f.client.Do(r)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, want, response.StatusCode)
			// Rejections must release permits and leave publication retryable.
			stageRequest(
				t,
				f,
				id,
				token,
				"upload",
				stageArchive(t, map[string]string{"file": "data"}),
				204,
			)
		})
	}
}
