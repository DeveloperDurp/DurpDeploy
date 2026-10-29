package agentserver_test

import (
	"context"
	"crypto/sha256"
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
	"github.com/stretchr/testify/require"
)

// Hold the first response write, rather than relying on socket buffer sizes.
type artifactAdmissionWriter struct {
	http.ResponseWriter
	request *http.Request
	started chan<- struct{}
	release <-chan struct{}
}

func (w *artifactAdmissionWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *artifactAdmissionWriter) Write(data []byte) (int, error) {
	if w.started != nil {
		w.started <- struct{}{}
		w.started = nil
		select {
		case <-w.release:
		case <-w.request.Context().Done():
			return 0, w.request.Context().Err()
		}
	}
	return w.ResponseWriter.Write(data)
}

func TestAgentArtifactAdmission(t *testing.T) {
	for _, limits := range []config.TransferLimits{
		config.Defaults().Limits.Transfer,
		{Global: 3, PerAgent: 2, IdleTimeoutSeconds: 300},
	} {
		t.Run(
			fmt.Sprintf("%d/%d", limits.Global, limits.PerAgent),
			func(t *testing.T) {
				checkArtifactAdmission(t, limits)
			},
		)
	}
}

func checkArtifactAdmission(t *testing.T, limits config.TransferLimits) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.Transfer = limits
	f, id, token := startedStagingAdmission(t, settings)
	var fetches atomic.Int64
	seedAgentArtifact(t, f, id, func() { fetches.Add(1) })
	workspace := t.TempDir()
	f.repo.ArtifactClient.TempDir = workspace
	peer := stagingAdmissionPeer(t, f, id, "overflow")
	started := make(chan struct{}, limits.Global)
	finished := make(chan struct{}, limits.Global)
	release := make(chan struct{})
	router := f.server.Config.Handler
	listener := httptest.NewUnstartedServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Transfer") == "hold" {
				defer func() { finished <- struct{}{} }()
				w = &artifactAdmissionWriter{w, r, started, release}
			}
			router.ServeHTTP(w, r)
		}))
	listener.TLS = f.server.TLS.Clone()
	listener.StartTLS()
	t.Cleanup(listener.Close)
	t.Cleanup(func() { close(release) })
	f.server = listener
	path := fmt.Sprintf("/agent/v1/deployments/%d/artifact", id)
	body := fmt.Sprintf(`{"claim_token":%q}`, token)
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})
	hold := func(client *http.Client, claim string) {
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		request, err := http.NewRequestWithContext(ctx, "POST",
			listener.URL+path, strings.NewReader(claim))
		require.NoError(t, err)
		request.Header.Set("X-Test-Transfer", "hold")
		go func() {
			response, err := client.Do(request)
			if err == nil {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("package response did not reach its first write")
		}
	}
	probe := func(client *http.Client, claim string) {
		t.Helper()
		before := fetches.Load()
		files, err := os.ReadDir(workspace)
		require.NoError(t, err)
		request, err := http.NewRequestWithContext(t.Context(), "POST",
			listener.URL+path, strings.NewReader(claim))
		require.NoError(t, err)
		response, err := client.Do(request)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, 429, response.StatusCode)
		require.Equal(t, "1", response.Header.Get("Retry-After"))
		require.True(t, response.Close)
		require.Equal(
			t,
			before,
			fetches.Load(),
			"rejected package fetched upstream",
		)
		after, err := os.ReadDir(workspace)
		require.NoError(t, err)
		require.Equal(t, files, after, "rejected package created a ZIP")
	}
	for range limits.PerAgent {
		hold(f.client, body)
	}
	probe(f.client, body)
	data := stageArchive(t, map[string]string{"result": "admission"})
	checkConfiguredTransferRejected(t, f, f, id, token, data)
	for i := limits.PerAgent; i < limits.Global; i++ {
		name := fmt.Sprintf("admitted-%d", i)
		other := stagingAdmissionPeer(t, f, id, name)
		hold(other.client, fmt.Sprintf(`{"claim_token":%q}`, "admission-"+name))
	}
	probe(peer.client, `{"claim_token":"admission-overflow"}`)
	checkConfiguredTransferRejected(t, f, peer, id, "admission-overflow", data)
	require.Zero(t, f.repo.StagingTransferDuration(db.GetStagingSnapshotParams{
		DeploymentID: id, Producer: "overflow",
	}, time.Now()), "rejected package paused execution timing")
	require.Equal(t, limits.Global, fetches.Load())
	// A new live claim must share capacity with a terminal run's active I/O.
	otherID := seedPollPayload(t, f, "pending", "test-agent")
	_, err := f.repo.DB.ExecContext(t.Context(), `
UPDATE deployments SET status='running' WHERE id=?`, otherID)
	require.NoError(t, err)
	_, err = f.repo.DB.ExecContext(t.Context(), `
UPDATE remote_step_runs SET state='succeeded'
WHERE deployment_id=? AND agent_id='test-agent'`, id)
	require.NoError(t, err)
	nextHash := sha256.Sum256([]byte("next-admission-token"))
	_, err = f.repo.DB.ExecContext(t.Context(), `INSERT INTO remote_step_runs
(deployment_id,step_index,agent_id,state,claim_token_hash,last_heartbeat_at)
SELECT ?,step_index,agent_id,'started',?,last_heartbeat_at
FROM remote_step_runs WHERE deployment_id=? AND agent_id='test-agent'`,
		otherID, nextHash[:], id)
	require.NoError(t, err)
	f.repo.ClearStagingTransfers(
		db.ListRemoteStepRunsForRunnerParams{DeploymentID: id},
	)
	response := postAgent(t, f,
		fmt.Sprintf("/agent/v1/deployments/%d/artifact", otherID),
		`{"claim_token":"next-admission-token"}`)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 429, response.StatusCode)
	require.Zero(t, f.repo.StagingTransferDuration(db.GetStagingSnapshotParams{
		DeploymentID: otherID, Producer: "test-agent",
	}, time.Now()))
	for _, cancel := range cancels {
		cancel()
	}
	for range limits.Global {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("disconnect retained package admission")
		}
	}
	files, err := os.ReadDir(workspace)
	require.NoError(t, err)
	require.Empty(t, files)
	_, err = f.repo.DB.ExecContext(t.Context(), `
UPDATE remote_step_runs SET state='cancelled' WHERE deployment_id=?`, otherID)
	require.NoError(t, err)
	_, err = f.repo.DB.ExecContext(t.Context(), `
UPDATE remote_step_runs SET state='started'
WHERE deployment_id=? AND agent_id='test-agent'`, id)
	require.NoError(t, err)
	// Staging capacity must also prevent package work, across endpoint types.
	done, ok := f.repo.TryBeginStagingTransfer(
		db.GetStagingSnapshotParams{Producer: "test-agent"},
	)
	require.True(t, ok)
	defer done()
	var extra []func()
	for i := int64(1); i < limits.PerAgent; i++ {
		finish, admitted := f.repo.TryBeginStagingTransfer(
			db.GetStagingSnapshotParams{Producer: "test-agent"},
		)
		require.True(t, admitted)
		extra = append(extra, finish)
		defer finish()
	}
	probe(f.client, body)
	done()
	for _, finish := range extra {
		finish()
	}
	response = postAgent(t, f, path, body)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 200, response.StatusCode)
}
