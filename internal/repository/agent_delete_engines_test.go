package repository

import (
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentDeleteHistoryAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		for _, outcome := range []string{"expired", "cleanup_unconfirmed"} {
			t.Run(outcome, func(t *testing.T) {
				testAgentDeleteEngineOutcome(t, repo, outcome)
			})
		}
	})
}

type agentDeleteEngineHistory struct {
	fixture revocationRaceFixture
	pairing db.AgentPairing
	outcome string
}

func seedAgentDeleteEngineHistory(
	t *testing.T,
	repo *Repository,
	outcome string,
) agentDeleteEngineHistory {
	t.Helper()
	// Given: terminal remote work, including reconciled cleanup.
	f := seedRevocationRaceFixture(t, repo, outcome)
	pairing, err := repo.Queries.GetAgentPairing(
		t.Context(),
		f.agentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ClaimRemoteDeployment(t.Context(),
		raceClaimParams(t, repo, f))
	if err != nil || rows != 1 {
		t.Fatalf("claim rows=%d error=%v", rows, err)
	}
	state := "succeeded"
	if outcome == "cleanup_unconfirmed" {
		state = outcome
	}
	if _, err := repo.DB.ExecContext(t.Context(),
		`UPDATE remote_deployment_claims SET state=?,started_at=100,
		finished_at=200 WHERE deployment_id=?`, state, f.deploymentID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{ID: f.deploymentID,
			Status: outcome, StartedAt: raceInt(100), FinishedAt: raceInt(200)}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.ConfirmRemoteDeploymentCleanup(t.Context(),
		db.ConfirmRemoteDeploymentCleanupParams{AgentID: f.agentID, Now: raceInt(201)}); err != nil {
		t.Fatal(err)
	}

	return agentDeleteEngineHistory{
		fixture: f,
		pairing: pairing,
		outcome: outcome,
	}
}

func testAgentDeleteEngineOutcome(
	t *testing.T,
	repo *Repository,
	outcome string,
) {
	t.Helper()
	given := seedAgentDeleteEngineHistory(t, repo, outcome)
	f, pairing := given.fixture, given.pairing
	// When
	if err := repo.DeleteAgent(t.Context(), f.agentID); err != nil {
		t.Fatal(err)
	}

	d := assertAgentDeleteEngineHistory(t, repo, given)
	assertAgentDeleteEngineMaintenance(t, repo, d)
	rejoined, err := repo.PrepareAgentPairing(
		t.Context(),
		AgentPairingTuple{
			AgentID: "new-" + f.agentID, AgentName: "new registration",
			Endpoint: "https://agent.invalid/" + f.agentID,
			AgentPin: pairing.AgentPin, PairingCodeHash: pairing.PairingCodeHash,
			AgentPublicIdentity:  pairing.AgentPublicIdentity,
			ServerPublicIdentity: pairing.ServerPublicIdentity.String,
			ServerPin:            pairing.ServerPin.String,
			EncryptedIdentity:    "new-ciphertext", Now: 300, ExpiresAt: 500,
		},
	)
	if err != nil || rejoined.AgentID != "new-"+f.agentID {
		t.Fatalf("new pairing=%+v error=%v", rejoined, err)
	}
}

func assertAgentDeleteEngineHistory(
	t *testing.T,
	repo *Repository,
	given agentDeleteEngineHistory,
) db.Deployment {
	t.Helper()
	f, outcome := given.fixture, given.outcome
	// Then: history keeps its outcome without registration or queue blockers.
	d, err := repo.Queries.GetDeployment(
		t.Context(),
		f.deploymentID,
	)
	if err != nil || d.Status != outcome ||
		d.AssignedAgentID.Valid {
		t.Fatalf("history=%+v error=%v", d, err)
	}
	if _, err := repo.Queries.GetAgent(t.Context(), f.agentID); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("retained registration: %v", err)
	}
	if _, err := repo.Queries.GetAgentPairing(t.Context(), f.agentID); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("retained pairing: %v", err)
	}
	blockers, err := repo.Queries.ListEnvironmentQueueBlockers(
		t.Context(),
		d.EnvironmentID,
	)
	if err != nil || len(blockers) != 0 {
		t.Fatalf("blockers=%v error=%v", blockers, err)
	}
	unconfirmed, err := repo.Queries.HasUnconfirmedContainerCleanup(
		t.Context(),
		d.ID,
	)
	if err != nil || unconfirmed != 0 {
		t.Fatalf("cleanup=%d error=%v", unconfirmed, err)
	}
	return d
}

func assertAgentDeleteEngineMaintenance(
	t *testing.T,
	repo *Repository,
	d db.Deployment,
) {
	t.Helper()
	for _, check := range []struct {
		name string
		run  func() (int64, error)
	}{
		{"environment", func() (int64, error) {
			return repo.Queries.HasActiveEnvironmentDeployment(t.Context(), d.EnvironmentID)
		}},
		{"release", func() (int64, error) {
			return repo.Queries.HasActiveReleaseDeployment(t.Context(), d.ReleaseID)
		}},
		{"project", func() (int64, error) {
			return repo.Queries.HasActiveProjectDeployment(t.Context(), 1)
		}},
	} {
		active, err := check.run()
		if err != nil || active != 0 {
			t.Fatalf(
				"%s active=%d error=%v",
				check.name,
				active,
				err,
			)
		}
	}

}
