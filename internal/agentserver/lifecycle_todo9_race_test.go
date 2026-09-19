package agentserver_test

import (
	"crypto/sha256"
	"errors"
	"testing"

	"durpdeploy/internal/logscrub"
	"durpdeploy/internal/repository"
)

func TestRevocationLogRaceHasOneWinner(t *testing.T) {
	t.Run("RevocationFirst", func(t *testing.T) {
		fixture := runningAgentFixture(t)
		revokeAgent(t, fixture)
		_, err := fixture.repo.AppendRemoteDeploymentLogs(
			t.Context(), lifecycleIdentity(),
			[]repository.RemoteLogEvent{{Sequence: 1, Line: "reject"}},
			logscrub.New(nil),
		)
		if !errors.Is(err, repository.ErrRemoteLifecycleConflict) {
			t.Fatalf("revocation-first log error=%v", err)
		}
		assertNoDeploymentLogs(t, fixture)
	})
	t.Run("LogFirst", func(t *testing.T) {
		fixture := runningAgentFixture(t)
		logs, err := fixture.repo.AppendRemoteDeploymentLogs(
			t.Context(), lifecycleIdentity(),
			[]repository.RemoteLogEvent{{Sequence: 1, Line: "kept"}},
			logscrub.New(nil),
		)
		if err != nil || len(logs) != 1 {
			t.Fatalf("log-first rows=%d error=%v", len(logs), err)
		}
		revokeAgent(t, fixture)
	})
}

func TestRevocationResultRaceHasOneWinner(t *testing.T) {
	t.Run("RevocationFirst", func(t *testing.T) {
		fixture := runningAgentFixture(t)
		revokeAgent(t, fixture)
		_, err := fixture.repo.FinishRemoteDeploymentLifecycle(
			t.Context(), lifecycleIdentity(), "succeeded",
		)
		if !errors.Is(err, repository.ErrRemoteLifecycleConflict) {
			t.Fatalf("revocation-first result error=%v", err)
		}
		assertClaimState(t, fixture, "started")
	})
	t.Run("ResultFirst", func(t *testing.T) {
		fixture := runningAgentFixture(t)
		result, err := fixture.repo.FinishRemoteDeploymentLifecycle(
			t.Context(), lifecycleIdentity(), "succeeded",
		)
		if err != nil || !result.Changed {
			t.Fatalf("result-first transition=%+v error=%v", result, err)
		}
		revokeAgent(t, fixture)
		assertClaimState(t, fixture, "succeeded")
	})
}

func TestRevocationCancelledRaceHasOneWinner(t *testing.T) {
	t.Run("RevocationFirst", func(t *testing.T) {
		fixture := cancellingAgentFixture(t)
		revokeAgent(t, fixture)
		changed, err := fixture.repo.AcknowledgeRemoteCancellation(
			t.Context(), cancellationIdentity(),
		)
		if !errors.Is(err, repository.ErrRemoteLifecycleConflict) || changed {
			t.Fatalf(
				"revocation-first cancelled changed=%v error=%v",
				changed,
				err,
			)
		}
	})
	t.Run("CancelledFirst", func(t *testing.T) {
		fixture := cancellingAgentFixture(t)
		changed, err := fixture.repo.AcknowledgeRemoteCancellation(
			t.Context(), cancellationIdentity(),
		)
		if err != nil || !changed {
			t.Fatalf("cancelled-first changed=%v error=%v", changed, err)
		}
		revokeAgent(t, fixture)
		assertClaimStateFor(t, fixture, 2, "cancelled")
	})
}

func runningAgentFixture(t *testing.T) agentFixture {
	t.Helper()
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	if err := fixture.repo.StartRemoteDeployment(
		t.Context(), lifecycleIdentity(),
	); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func cancellingAgentFixture(t *testing.T) agentFixture {
	t.Helper()
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	releaseFirstClaim(t, fixture)
	activateCancellationFixture(t, fixture.repo)
	return fixture
}

func releaseFirstClaim(t *testing.T, fixture agentFixture) {
	t.Helper()
	_, err := fixture.repo.DB.Exec(`UPDATE remote_deployment_claims SET
		state='waiting',claim_token_hash=NULL,ciphertext=NULL,
		claim_expires_at=NULL,last_heartbeat_at=NULL,updated_at=1
		WHERE deployment_id=1`)
	if err != nil {
		t.Fatal(err)
	}
}

func cancellationIdentity() repository.RemoteLifecycleClaim {
	hash := sha256.Sum256([]byte("claim-two"))
	return repository.RemoteLifecycleClaim{
		DeploymentID:   2,
		AgentID:        "test-agent",
		ClaimTokenHash: hash[:],
	}
}

func assertClaimStateFor(
	t *testing.T,
	fixture agentFixture,
	deploymentID int64,
	want string,
) {
	t.Helper()
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(
		t.Context(), deploymentID,
	)
	if err != nil || claim.State != want {
		t.Fatalf("claim state=%q want=%q error=%v", claim.State, want, err)
	}
}
