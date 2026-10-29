package agentserver_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type artifactClaimLookup struct {
	*sql.DB
	fail bool
}

func (q artifactClaimLookup) QueryRowContext(
	ctx context.Context, query string, args ...any,
) *sql.Row {
	if strings.Contains(query, "-- name: GetRemoteStepRunByClaim") {
		if !q.fail {
			return q.DB.QueryRowContext(ctx, "SELECT 1 WHERE 0")
		}
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return q.DB.QueryRowContext(ctx, query, args...)
	}
	return q.DB.QueryRowContext(ctx, query, args...)
}

func TestAgentArtifactAdmissionReleases(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status int
	}{
		{"success", 200}, {"upstream", 502}, {"checksum", 422},
		{"missing pin", 404}, {"expired", 409}, {"cancelled", 409},
		{"removed claim", 409}, {"lookup failure", 500},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			checkAgentArtifactAdmissionRelease(
				t,
				scenario.name,
				scenario.status,
			)
		})
	}
}

func checkAgentArtifactAdmissionRelease(
	t *testing.T, scenario string, status int,
) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.Transfer.Global = 1
	f, id, token := startedStagingAdmission(t, settings)
	var fetches atomic.Int64
	var recovered atomic.Bool
	data := seedAgentArtifact(t, f, id, func() {
		fetches.Add(1)
		if recovered.Load() {
			return
		}
		query := ""
		switch scenario {
		case "expired":
			query = "UPDATE remote_step_runs SET last_heartbeat_at=0"
		case "cancelled":
			query = "UPDATE remote_step_runs SET state='cancelled'"
		}
		if query != "" {
			_, err := f.repo.DB.ExecContext(t.Context(), query)
			assert.NoError(t, err)
		}
	})
	workspace := t.TempDir()
	f.repo.ArtifactClient.TempDir = workspace
	upstreamClient := f.repo.ArtifactClient.HTTP
	switch scenario {
	case "upstream":
		f.repo.ArtifactClient.HTTP = &http.Client{
			Transport: artifactFailureTransport{},
		}
	case "checksum":
		_, err := f.repo.DB.ExecContext(
			t.Context(),
			"UPDATE deployment_artifacts SET sha256=?",
			strings.Repeat("0", 64),
		)
		require.NoError(t, err)
	case "missing pin":
		_, err := f.repo.DB.ExecContext(
			t.Context(),
			"DELETE FROM deployment_artifacts",
		)
		require.NoError(t, err)
	case "removed claim", "lookup failure":
		f.repo.Queries = db.New(artifactClaimLookup{
			DB: f.repo.DB, fail: scenario == "lookup failure",
		})
	}
	finished := make(chan struct{}, 1)
	router := f.server.Config.Handler
	listener := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			defer func() { finished <- struct{}{} }()
			router.ServeHTTP(w, r)
		}))
	listener.TLS = f.server.TLS.Clone()
	listener.StartTLS()
	t.Cleanup(listener.Close)
	f.server = listener
	response := postAgent(t, f,
		fmt.Sprintf("/agent/v1/deployments/%d/artifact", id),
		fmt.Sprintf(`{"claim_token":%q}`, token))
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, status, response.StatusCode)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("artifact handler did not finish")
	}
	files, err := os.ReadDir(workspace)
	require.NoError(t, err)
	require.Empty(t, files)
	done, ok := f.repo.TryBeginStagingTransfer(
		db.GetStagingSnapshotParams{Producer: "test-agent"},
	)
	require.True(t, ok, "handler retained admission")
	done()
	if status == 200 {
		require.Equal(t, data, body)
	}
	if scenario == "removed claim" ||
		scenario == "lookup failure" {
		require.Zero(t, fetches.Load())
		require.Zero(
			t,
			f.repo.StagingTransferDuration(db.GetStagingSnapshotParams{
				DeploymentID: id, Producer: "test-agent",
			}, time.Now()),
		)
	}
	// A later legitimate request must work after every failure path.
	recovered.Store(true)
	f.repo.Queries = db.New(f.repo.DB)
	f.repo.ArtifactClient.HTTP = upstreamClient
	_, err = f.repo.DB.ExecContext(
		t.Context(),
		"UPDATE remote_step_runs SET state='started',last_heartbeat_at=?",
		time.Now().Unix(),
	)
	require.NoError(t, err)
	deployment, err := f.repo.Queries.GetDeployment(t.Context(), id)
	require.NoError(t, err)
	_, err = f.repo.DB.ExecContext(
		t.Context(),
		"DELETE FROM deployment_artifacts",
	)
	require.NoError(t, err)
	require.NoError(
		t,
		f.repo.Queries.CopyReleaseArtifactToDeployment(
			t.Context(),
			db.CopyReleaseArtifactToDeploymentParams{
				DeploymentID: id,
				ReleaseID:    deployment.ReleaseID,
			},
		),
	)
	response = postAgent(t, f,
		fmt.Sprintf("/agent/v1/deployments/%d/artifact", id),
		fmt.Sprintf(`{"claim_token":%q}`, token))
	body, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 200, response.StatusCode)
	require.Equal(t, data, body)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("retried artifact handler did not finish")
	}
}

type artifactFailureTransport struct{}

func (artifactFailureTransport) RoundTrip(
	*http.Request,
) (*http.Response, error) {
	return nil, io.ErrUnexpectedEOF
}
