package repository

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestAgentPolicyRecordAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, peer := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		testAgentPolicyRecord(t, repo, peer)
	})
}

func TestRePairClearsRecordedAgentPolicy(t *testing.T) {
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	tuple := pairingRecoveryTuple(t, repo, "revoked")
	advertisement, err := repo.ResourceLimits().AgentPolicy().Advertisement()
	require.NoError(t, err)
	// Model a retained capability from an older revoked registration.
	err = repo.Queries.AddAgentResourcePolicy(
		t.Context(),
		db.AddAgentResourcePolicyParams{
			AgentID: recoveryAgentID, Advertisement: advertisement,
		},
	)
	require.NoError(t, err)
	_, err = repo.PrepareAgentPairing(t.Context(), tuple)
	require.NoError(t, err)
	_, err = repo.Queries.GetAgentResourcePolicy(t.Context(), recoveryAgentID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func testAgentPolicyRecord(t *testing.T, repo, peer *Repository) {
	t.Helper()
	f := seedRevocationRaceFixture(t, repo, "policy-record")
	agent, err := repo.Queries.GetAgent(t.Context(), f.agentID)
	require.NoError(t, err)
	heartbeat := db.HeartbeatAgentParams{
		ID: f.agentID, CertificateFingerprint: agent.CertificateFingerprint,
		Now: raceInt(200), AgentVersion: raceString("legacy"),
	}
	report := AgentPollCapabilities{PollRequest: agentproto.PollRequest{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{
			Protocol: agentproto.AgentV1,
		},
	}}
	n, err := repo.RecordAgentPollCapabilities(t.Context(), heartbeat, report)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	_, err = peer.Queries.GetAgentResourcePolicy(t.Context(), f.agentID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	before, err := peer.Queries.GetAgent(t.Context(), f.agentID)
	require.NoError(t, err)
	advertisement, err := repo.ResourceLimits().AgentPolicy().Advertisement()
	require.NoError(t, err)
	report.ResourcePolicy = advertisement
	report.Protocol = agentproto.AgentV2
	report.SupportedInterpreters = []agentproto.Interpreter{"bash", "invalid"}
	heartbeat.Now, heartbeat.AgentVersion = raceInt(
		201,
	), raceString(
		"invalid-capability",
	)
	_, err = repo.RecordAgentPollCapabilities(t.Context(), heartbeat, report)
	require.Error(t, err)
	_, err = peer.Queries.GetAgentResourcePolicy(t.Context(), f.agentID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	after, err := peer.Queries.GetAgent(t.Context(), f.agentID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	report.SupportedInterpreters = []agentproto.Interpreter{"bash"}
	heartbeat.AgentVersion = raceString("modern")
	n, err = repo.RecordAgentPollCapabilities(t.Context(), heartbeat, report)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	// A separate repository and database connection read the durable snapshot.
	restarted := New(peer.DB)
	stored, err := restarted.Queries.GetAgentResourcePolicy(
		t.Context(),
		f.agentID,
	)
	require.NoError(t, err)
	require.Equal(t, advertisement, stored)
	report.ResourcePolicy = "untrusted-policy"
	heartbeat.Now = raceInt(202)
	_, err = repo.RecordAgentPollCapabilities(t.Context(), heartbeat, report)
	require.ErrorIs(t, err, resourcepolicy.ErrIncompatible)
	stored, err = restarted.Queries.GetAgentResourcePolicy(
		t.Context(),
		f.agentID,
	)
	require.NoError(t, err)
	require.Equal(t, advertisement, stored)
	report.ResourcePolicy = ""
	n, err = repo.RecordAgentPollCapabilities(t.Context(), heartbeat, report)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	_, err = restarted.Queries.GetAgentResourcePolicy(t.Context(), f.agentID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	settings := config.Defaults()
	settings.Limits.Execution.MaxRetries++
	custom := NewConfigured(repo.DB, settings)
	_, err = custom.RecordAgentPollCapabilities(t.Context(), heartbeat, report)
	require.ErrorIs(t, err, resourcepolicy.ErrIncompatible)
	report.ResourcePolicy, err = settings.Limits.AgentPolicy().Advertisement()
	require.NoError(t, err)
	n, err = custom.RecordAgentPollCapabilities(t.Context(), heartbeat, report)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	stored, err = restarted.Queries.GetAgentResourcePolicy(
		t.Context(),
		f.agentID,
	)
	require.NoError(t, err)
	require.Equal(t, report.ResourcePolicy, stored)
	_, err = custom.RevokeAgent(t.Context(), f.agentID)
	require.NoError(t, err)
	_, err = restarted.Queries.GetAgentResourcePolicy(t.Context(), f.agentID)
	require.ErrorIs(t, err, sql.ErrNoRows)
}
