package repository_test

import (
	"testing"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/stretchr/testify/require"
)

func TestCustomPIDPolicyBlocksRemoteQueueAndClaims(t *testing.T) {
	for _, helper := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "workload", true: "helper"}[helper],
			func(t *testing.T) {
				limits := config.Defaults().Limits.Container
				if helper {
					limits.HelperPIDs = 8
				} else {
					limits.PIDs = 160
				}
				testCustomPIDPolicyBlocksRemoteQueueAndClaims(
					t,
					limits,
				)
			},
		)
	}
}

func TestCustomContainerResourcePolicyBlocksRemoteQueueAndClaims(t *testing.T) {
	for _, field := range []string{"tmpfs", "helper CPUs", "helper memory"} {
		t.Run(field, func(t *testing.T) {
			limits := config.Defaults().Limits.Container
			switch field {
			case "tmpfs":
				limits.TmpfsBytes = 1 << 20
			case "helper CPUs":
				limits.HelperCPUs = 0.5
			case "helper memory":
				limits.HelperMemoryOverheadBytes = 8 << 20
			}
			testCustomPIDPolicyBlocksRemoteQueueAndClaims(
				t,
				limits,
			)
		})
	}
}

func testCustomPIDPolicyBlocksRemoteQueueAndClaims(
	t *testing.T, limits config.ContainerLimits,
) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.Container = limits
	testCustomResourcePolicyBlocksRemoteQueueAndClaims(t, settings, false)
}

func testCustomResourcePolicyBlocksRemoteQueueAndClaims(
	t *testing.T, settings config.Config, withPackage bool,
) {
	t.Helper()
	r := remoteFixture(t)
	configured := repository.NewConfigured(r.DB, settings)
	seedPolicyStep(t, r, withPackage)
	n, err := configured.QueueRemoteStepRuns(t.Context(), 3, 0)
	require.Zero(t, n)
	require.ErrorIs(t, err, agentexecution.ErrLimits)
	require.ErrorContains(t, err, "resource policy")
	runs, err := r.Queries.ListRemoteStepRuns(
		t.Context(),
		db.ListRemoteStepRunsParams{
			DeploymentID: 3, StepIndex: 0,
		},
	)
	require.NoError(t, err)
	require.Empty(t, runs, "rejected queue left partial fan-out runs")
	prepare := func(repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
		t.Fatal("unsupported resource policy reached payload encryption")
		return repository.RemotePreparedClaim{}, nil
	}
	_, claimed, err := configured.ClaimRemoteDeploymentPayload(
		t.Context(),
		"a",
		prepare,
	)
	require.NoError(t, err)
	require.False(t, claimed)
	n, err = configured.ClaimRemoteDeployment(
		t.Context(),
		currentClaimArg(t, r),
	)
	require.Zero(t, n)
	require.ErrorIs(t, err, agentexecution.ErrLimits)
	_, err = r.DB.ExecContext(
		t.Context(),
		`INSERT INTO remote_step_runs(deployment_id,step_index,agent_id) VALUES(3,0,'a')`,
	)
	require.NoError(t, err)
	_, claimed, err = configured.ClaimRemoteStepPayload(
		t.Context(),
		"a",
		prepare,
	)
	require.NoError(t, err)
	require.False(t, claimed)
	var ciphertext int
	require.NoError(
		t,
		r.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM remote_step_runs WHERE ciphertext IS NOT NULL`).
			Scan(&ciphertext),
	)
	require.Zero(t, ciphertext)
	var logs int
	require.NoError(
		t,
		r.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM deployment_logs WHERE line LIKE '%resource policy%'`).
			Scan(&logs),
	)
	require.GreaterOrEqual(t, logs, 2)
	testMatchingResourcePolicyClaims(t, settings, withPackage)
}

func seedPolicyStep(t *testing.T, r *repository.Repository, withPackage bool) {
	t.Helper()
	_, err := r.DB.ExecContext(t.Context(), `
INSERT INTO agent_labels VALUES('a','other');
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('a',2);
UPDATE deployments SET status='running' WHERE id=3;
UPDATE deployment_steps SET max_retries=0;`)
	require.NoError(t, err)
	if withPackage {
		_, err := r.DB.ExecContext(t.Context(), `
INSERT INTO agent_artifact_capabilities(agent_id) VALUES('a');
INSERT INTO package_repositories(id,project_id,name,url_template,auth_type) VALUES(1,1,'fixture','https://fixture.invalid/{version}.zip','noauth');
INSERT INTO deployment_artifacts(deployment_id,repository_id,url,version,sha256,size) SELECT id,1,'https://fixture.invalid/1.zip','1','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',100 FROM deployments;`)
		require.NoError(t, err)
	}
}

func testMatchingResourcePolicyClaims(
	t *testing.T,
	settings config.Config,
	withPackage bool,
) {
	t.Helper()
	r := remoteFixture(t)
	configured := repository.NewConfigured(r.DB, settings)
	seedPolicyStep(t, r, withPackage)
	if withPackage &&
		settings.Limits.Artifact.DownloadBytes > config.Defaults().Limits.Artifact.DownloadBytes {
		_, err := r.DB.ExecContext(
			t.Context(),
			"UPDATE deployment_artifacts SET size=?",
			settings.Limits.Artifact.DownloadBytes,
		)
		require.NoError(t, err)
	}
	agent, err := r.Queries.GetAgent(t.Context(), "a")
	require.NoError(t, err)
	advertisement, err := settings.Limits.AgentPolicy().Advertisement()
	require.NoError(t, err)
	n, err := configured.RecordAgentPollCapabilities(
		t.Context(),
		db.HeartbeatAgentParams{
			ID: "a", CertificateFingerprint: agent.CertificateFingerprint,
			Now: ni(200),
		},
		repository.AgentPollCapabilities{
			PollRequest: agentproto.PollRequest{
				ProtocolEnvelope: agentproto.ProtocolEnvelope{
					Protocol: agentproto.AgentV1,
				},
			},
			ResourcePolicy: advertisement, Artifact: withPackage,
		},
	)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = configured.QueueRemoteStepRuns(t.Context(), 3, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	_, claimed, err := configured.ClaimRemoteStepPayload(
		t.Context(),
		"a",
		func(snapshot repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
			require.Equal(t, advertisement, snapshot.ResourcePolicy)
			return repository.RemotePreparedClaim{
				Token: "fixture", TokenHash: make([]byte, 32), Ciphertext: []byte("fixture"),
			}, nil
		},
	)
	require.NoError(t, err)
	require.True(t, claimed)
}
