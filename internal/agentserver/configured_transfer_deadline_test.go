package agentserver_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredTransferDeadlinesAPIE2E(t *testing.T) {
	for _, seconds := range []int64{1, 600} {
		for _, action := range []string{"upload", "download", "artifact"} {
			t.Run(fmt.Sprintf("%d/%s", seconds, action), func(t *testing.T) {
				checkConfiguredTransferDeadline(t, seconds, action)
			})
		}
	}
}

func checkConfiguredTransferDeadline(
	t *testing.T,
	seconds int64,
	action string,
) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.Transfer.IdleTimeoutSeconds = seconds
	f, id, token := startedStagingAdmission(t, settings)
	var artifactBody string
	if action == "artifact" {
		seedAgentArtifact(t, f, id, nil)
		artifactBody = fmt.Sprintf(`{"claim_token":%q}`, token)
	}
	finished := make(chan bool, 1)
	router := f.server.Config.Handler
	listener := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			writer := &configuredDeadlineWriter{
				ResponseWriter: w, t: t, timeout: time.Duration(seconds) * time.Second,
			}
			defer func() { finished <- writer.observed }()
			router.ServeHTTP(writer, r)
		}))
	listener.TLS = f.server.TLS.Clone()
	listener.StartTLS()
	t.Cleanup(listener.Close)
	f.server = listener
	if action == "artifact" {
		response := postAgent(
			t,
			f,
			fmt.Sprintf("/agent/v1/deployments/%d/artifact", id),
			artifactBody,
		)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)
		require.NotEmpty(t, body)
	} else {
		data := stageArchive(t, map[string]string{"result": "deadline"})
		want := 204
		if action == "download" {
			data, want = nil, 200
		}
		stageRequest(t, f, id, token, action, data, want)
	}
	require.True(
		t,
		<-finished,
		"configured socket write deadline was not applied",
	)
}

type configuredDeadlineWriter struct {
	http.ResponseWriter
	t        *testing.T
	timeout  time.Duration
	observed bool
}

func (w *configuredDeadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *configuredDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		w.observed = true
		if delta := time.Until(deadline) - w.timeout; delta < -250*time.Millisecond ||
			delta > 250*time.Millisecond {
			w.t.Errorf(
				"socket deadline differs from captured timeout: %s",
				delta,
			)
		}
	}
	return http.NewResponseController(w.ResponseWriter).
		SetWriteDeadline(deadline)
}
