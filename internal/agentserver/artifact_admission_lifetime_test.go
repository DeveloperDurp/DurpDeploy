package agentserver_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type artifactLifetimeWriter struct {
	http.ResponseWriter
	t         *testing.T
	repo      *repository.Repository
	directory string
	failure   string
	wrote     bool
	finalized bool
	deadline  time.Time
}

func (w *artifactLifetimeWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *artifactLifetimeWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	if w.wrote && !deadline.IsZero() {
		// stream.finish must run after ZIP cleanup, while capacity is owned.
		files, err := os.ReadDir(w.directory)
		assert.NoError(w.t, err)
		assert.Empty(w.t, files)
		done, ok := w.repo.TryBeginStagingTransfer(
			db.GetStagingSnapshotParams{Producer: "observer"},
		)
		if ok {
			done()
			w.t.Error("capacity released before stream finalization")
		}
		w.finalized = true
	}
	return http.NewResponseController(w.ResponseWriter).
		SetWriteDeadline(deadline)
}

func (w *artifactLifetimeWriter) Write(data []byte) (int, error) {
	w.wrote = true
	switch w.failure {
	case "disconnect":
		return 0, io.ErrClosedPipe
	case "idle":
		// Model a socket write waiting until the captured inactivity deadline.
		timer := time.NewTimer(time.Until(w.deadline))
		defer timer.Stop()
		<-timer.C
		return 0, os.ErrDeadlineExceeded
	default:
		return w.ResponseWriter.Write(data)
	}
}

func TestAgentArtifactAdmissionLifetime(t *testing.T) {
	for _, failure := range []string{"success", "disconnect", "idle"} {
		t.Run(failure, func(t *testing.T) {
			settings := config.Defaults()
			settings.Limits.Transfer.Global = 1
			settings.Limits.Transfer.IdleTimeoutSeconds = 1
			f, id, token := startedStagingAdmission(t, settings)
			seedAgentArtifact(t, f, id, nil)
			workspace := t.TempDir()
			f.repo.ArtifactClient.TempDir = workspace
			finished := make(chan bool, 1)
			router := f.server.Config.Handler
			listener := httptest.NewUnstartedServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					writer := &artifactLifetimeWriter{
						ResponseWriter: w, t: t, repo: f.repo,
						directory: workspace, failure: failure,
					}
					defer func() { finished <- writer.finalized }()
					router.ServeHTTP(writer, r)
				}))
			listener.TLS = f.server.TLS.Clone()
			listener.StartTLS()
			t.Cleanup(listener.Close)
			f.server = listener
			request, err := http.NewRequestWithContext(
				t.Context(),
				"POST",
				fmt.Sprintf(
					"%s/agent/v1/deployments/%d/artifact",
					listener.URL,
					id,
				),
				strings.NewReader(fmt.Sprintf(`{"claim_token":%q}`, token)),
			)
			require.NoError(t, err)
			response, err := f.client.Do(request)
			if failure == "success" {
				require.NoError(t, err)
				require.Equal(t, 200, response.StatusCode)
			}
			if err == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				require.NoError(t, response.Body.Close())
			}
			select {
			case finalized := <-finished:
				require.True(t, finalized)
			case <-time.After(5 * time.Second):
				t.Fatal("artifact response did not finalize")
			}
			done, ok := f.repo.TryBeginStagingTransfer(
				db.GetStagingSnapshotParams{Producer: "test-agent"},
			)
			require.True(t, ok, "stream finalization retained capacity")
			done()
		})
	}
}
