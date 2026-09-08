package dispatch

import (
	"context"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestRuntimeMaintenanceExpiresPairings(t *testing.T) {
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	ctx := context.Background()
	_, err = repo.Queries.CreateAgent(ctx, db.CreateAgentParams{
		ID: "expired", Name: "expired", Endpoint: "https://agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Queries.CreateAgentPairing(ctx, db.CreateAgentPairingParams{
		AgentID: "expired", PairingCodeHash: make([]byte, 32),
		AgentPublicIdentity: "fixture", AgentPin: strings.Repeat("a", 64),
		ExpiresAt: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := New(repo).Maintain(ctx, 100); err != nil {
		t.Fatal(err)
	}
	pairing, err := repo.Queries.GetAgentPairing(ctx, "expired")
	if err != nil || pairing.State != "expired" {
		t.Fatalf("pairing state=%s err=%v", pairing.State, err)
	}
	t.Log("maintenance pass: expired pairing at deadline=100")
}
