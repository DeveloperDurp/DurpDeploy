package agentserver_test

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestStartCancelRaceHasOneWinner(t *testing.T) {
	t.Run("StartFirst", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		startLifecycle(t, fixture, http.StatusNoContent)
		if err := cancelFirstDeployment(t, fixture); err != nil {
			t.Fatal(err)
		}
		assertClaimState(t, fixture, "cancel_requested")
	})
	t.Run("CancelFirst", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		if err := cancelFirstDeployment(t, fixture); err != nil {
			t.Fatal(err)
		}
		startLifecycle(t, fixture, http.StatusConflict)
		assertClaimState(t, fixture, "cancelled")
	})
}

func TestResultCancelRaceHasOneWinner(t *testing.T) {
	t.Run("ResultFirst", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		startLifecycle(t, fixture, http.StatusNoContent)
		resultLifecycle(t, fixture, http.StatusNoContent)
		if err := cancelFirstDeployment(t, fixture); !errors.Is(
			err, repository.ErrRemoteLifecycleConflict,
		) {
			t.Fatalf("cancel after result error=%v", err)
		}
		assertClaimState(t, fixture, "succeeded")
	})
	t.Run("CancelFirst", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		startLifecycle(t, fixture, http.StatusNoContent)
		if err := cancelFirstDeployment(t, fixture); err != nil {
			t.Fatal(err)
		}
		resultLifecycle(t, fixture, http.StatusConflict)
		assertClaimState(t, fixture, "cancel_requested")
	})
}

func TestRevocationStartRaceRejectsLifecycleWrite(t *testing.T) {
	t.Run("RevocationFirst", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		revokeAgent(t, fixture)
		err := fixture.repo.StartRemoteDeployment(
			t.Context(),
			lifecycleIdentity(),
		)
		if !errors.Is(err, repository.ErrRemoteLifecycleConflict) {
			t.Fatalf("revocation-first start error=%v", err)
		}
		assertClaimState(t, fixture, "claimed")
	})
	t.Run("StartFirst", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		startLifecycle(t, fixture, http.StatusNoContent)
		revokeAgent(t, fixture)
		assertClaimState(t, fixture, "started")
	})
}

func TestRevocationHeartbeatRaceRejectsLifecycleWrite(t *testing.T) {
	t.Run("RevocationFirst", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		startLifecycle(t, fixture, http.StatusNoContent)
		revokeAgent(t, fixture)
		_, err := fixture.repo.HeartbeatRemoteDeployment(
			t.Context(), lifecycleIdentity())
		if !errors.Is(err, repository.ErrRemoteLifecycleConflict) {
			t.Fatalf("revocation-first heartbeat error=%v", err)
		}
	})
	t.Run("HeartbeatFirst", func(t *testing.T) {
		fixture := newAgentFixture(t)
		seedAgentLifecycle(t, fixture.repo)
		startLifecycle(t, fixture, http.StatusNoContent)
		heartbeatLifecycle(t, fixture, http.StatusOK)
		revokeAgent(t, fixture)
	})
}

func startLifecycle(t *testing.T, fixture agentFixture, want int) {
	t.Helper()
	response := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.StartPath, "{id}", "1",
	), `{"protocol":"agent/1","claim_token":"claim-one"}`)
	if response.StatusCode != want {
		t.Fatalf("start status=%d want=%d", response.StatusCode, want)
	}
}

func heartbeatLifecycle(t *testing.T, fixture agentFixture, want int) {
	t.Helper()
	response := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.HeartbeatPath, "{id}", "1",
	), `{"protocol":"agent/1","claim_token":"claim-one"}`)
	if response.StatusCode != want {
		t.Fatalf("heartbeat status=%d want=%d", response.StatusCode, want)
	}
}

func resultLifecycle(t *testing.T, fixture agentFixture, want int) {
	t.Helper()
	response := postAgent(t, fixture, strings.ReplaceAll(
		agentproto.ResultPath, "{id}", "1",
	), `{"protocol":"agent/1","claim_token":"claim-one",`+
		`"state":"succeeded","error":""}`)
	if response.StatusCode != want {
		t.Fatalf("result status=%d want=%d", response.StatusCode, want)
	}
}

func cancelFirstDeployment(t *testing.T, fixture agentFixture) error {
	t.Helper()
	return fixture.repo.CancelAssignedRemoteDeployment(t.Context(),
		repository.RemoteAssignedDeployment{
			DeploymentID: 1,
			AgentID:      "test-agent",
		})
}

func revokeAgent(t *testing.T, fixture agentFixture) {
	t.Helper()
	if _, err := fixture.repo.Queries.SetAgentStatus(
		t.Context(),
		db.SetAgentStatusParams{
			ID:     "test-agent",
			Status: "revoked",
		},
	); err != nil {
		t.Fatal(err)
	}
}

func assertClaimState(t *testing.T, fixture agentFixture, want string) {
	t.Helper()
	claim, err := fixture.repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != want {
		t.Fatalf("claim state=%q want=%q error=%v", claim.State, want, err)
	}
}

func lifecycleIdentity() repository.RemoteLifecycleClaim {
	hash := sha256.Sum256([]byte("claim-one"))
	return repository.RemoteLifecycleClaim{
		DeploymentID:   1,
		AgentID:        "test-agent",
		ClaimTokenHash: hash[:],
	}
}
