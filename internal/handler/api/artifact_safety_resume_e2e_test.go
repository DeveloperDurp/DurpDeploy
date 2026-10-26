//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"
)

func TestConfiguredArtifactApprovalResumeAPIE2E(t *testing.T) {
	for _, nodes := range []int64{1, 2} {
		t.Run(
			fmt.Sprint(nodes),
			func(t *testing.T) { checkArtifactApprovalResumePolicy(t, nodes, nil, nodes == 2) },
		)
	}
}

func TestConfiguredStagingApprovalResumeAPIE2E(t *testing.T) {
	for _, tc := range []struct {
		payload, entries, archive int64
		accepted                  bool
	}{{1, 2, 0, false}, {1024, 1, 0, false}, {1024, 2, 0, true}, {1024, 2, 3071, false}, {1024, 2, 3072, true}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			limits := config.Defaults().Limits.Staging
			limits.PayloadBytes, limits.Entries = tc.payload, tc.entries
			if tc.archive != 0 {
				limits.ArchiveBytes = tc.archive
			}
			checkArtifactApprovalResumePolicy(t, 2, &limits, tc.accepted)
		})
	}
}

func checkArtifactApprovalResumePolicy(
	t *testing.T,
	nodes int64,
	stagingPolicy *config.StagingLimits,
	accepted bool,
) {
	t.Helper()
	s := config.Defaults()
	s.Container.Runtime = os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	s.Container.Namespace = "artifact-resume-" + uuid.NewString()
	s.Limits.Artifact.Files = 1
	s.Limits.Artifact.StagingNodes = 2
	f := newArtifactPolicyFixture(t, s)
	f.environment = seedEnv(t, f.h.repo)
	f.done = make(chan events.Event, 16)
	f.completionTimeout = time.Minute
	bus := events.NewBus(f.h.repo)
	bus.Register(artifactNotifier{f.done})
	f.h.runner.SetEventBus(bus)
	_, deployment, gate := createGateDeployment(t, f)
	arg := db.GetArtifactGateParams{DeploymentID: deployment.ID, StepIndex: 0}
	before, err := f.h.repo.Queries.GetArtifactGate(t.Context(), arg)
	require.NoError(t, err)
	chunkArg := db.GetArtifactGateChunkParams{
		DeploymentID: deployment.ID,
		StepIndex:    0,
		ChunkIndex:   0,
	}
	chunk, err := f.h.repo.Queries.GetArtifactGateChunk(t.Context(), chunkArg)
	require.NoError(t, err)
	f.h.runner.KillAll()
	s, err = config.LoadWithLookup(f.h.repo.StartupLookup)
	require.NoError(t, err)
	s.Limits = f.h.repo.ResourceLimits()
	s.Limits.Artifact.StagingNodes = nodes
	if stagingPolicy != nil {
		s.Limits.Staging = *stagingPolicy
	}
	r := repository.NewConfigured(f.h.repo.DB, s)
	box, err := secret.NewBox(make([]byte, 32))
	require.NoError(t, err)
	r.SetSecretBox(box)
	r.ArtifactClient.HTTP.Transport.(*http.Transport).TLSClientConfig = f.h.repo.ArtifactClient.HTTP.Transport.(*http.Transport).TLSClientConfig.Clone()
	rnr := runner.New(r, runner.NewLogBroker())
	t.Cleanup(rnr.KillAll)
	srv := httptest.NewServer(
		server.NewRouter(
			r,
			rnr,
			cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
			handler.NewAuthHandler(r),
		),
	)
	t.Cleanup(srv.Close)
	f.h.repo = r
	f.h.runner = rnr
	f.baseURL = srv.URL
	f.client = srv.Client()
	if stagingPolicy != nil && stagingPolicy.ArchiveBytes < before.BundleSize {
		f.api(
			t,
			"POST",
			gateAPIPath(deployment.ID)+"/0/approve",
			map[string]any{
				"sha256":   gate.SHA256,
				"revision": gate.Revision,
			},
			409,
		)
		after, err := r.Queries.GetArtifactGate(t.Context(), arg)
		require.NoError(t, err)
		require.Equal(t, before, after)
		afterChunk, err := r.Queries.GetArtifactGateChunk(t.Context(), chunkArg)
		require.NoError(t, err)
		require.Equal(t, chunk, afterChunk)
		var attempts int
		require.NoError(t, r.DB.QueryRowContext(
			t.Context(),
			"SELECT COUNT(*) FROM deployment_logs WHERE deployment_id=? AND step_index=1 AND step_state='running'",
			deployment.ID,
		).Scan(&attempts))
		require.Zero(t, attempts)
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/cancel", deployment.ID),
			nil,
			200,
		)
		waitVerificationStatus(t, f, deployment.ID, "cancelled")
		assertStagingPolicyCleanup(t, f)
		return
	}
	f.api(
		t,
		"POST",
		gateAPIPath(deployment.ID)+"/0/approve",
		map[string]any{"sha256": gate.SHA256, "revision": gate.Revision},
		200,
	)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	status := ""
	require.Eventually(t, func() bool {
		var d db.Deployment
		require.NoError(t, json.Unmarshal(f.api(t, "GET", path, nil, 200), &d))
		status = d.Status
		return status == "failed" || status == "succeeded" ||
			status == "cancelled"
	}, 45*time.Second, 50*time.Millisecond)
	want := "failed"
	if accepted {
		want = "succeeded"
	}
	require.Equal(t, want, status)
	logs := string(f.api(t, "GET", path+"/logs", nil, 200))
	require.NotContains(t, logs, "applied")
	after, err := r.Queries.GetArtifactGate(t.Context(), arg)
	require.NoError(t, err)
	require.Equal(t, before.BundleSha256, after.BundleSha256)
	require.Equal(t, before.BundleSize, after.BundleSize)
	afterChunk, err := r.Queries.GetArtifactGateChunk(t.Context(), chunkArg)
	require.NoError(t, err)
	require.Equal(t, chunk, afterChunk)
	var attempts int
	require.NoError(
		t,
		r.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM deployment_logs WHERE deployment_id = ? AND step_index = 1 AND step_state = 'running'", deployment.ID).
			Scan(&attempts),
	)
	if !accepted {
		message := "Saved approval context does not satisfy the current staging-node limit"
		if stagingPolicy != nil {
			message = "approval context does not satisfy the current staging policy"
		}
		require.Contains(t, logs, message)
		require.Zero(t, attempts)
	} else {
		require.Positive(t, attempts)
	}
}
