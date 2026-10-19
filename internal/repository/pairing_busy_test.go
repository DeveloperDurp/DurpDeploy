package repository

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/testutil"
)

func TestPrepareAgentPairingRetriesSQLiteBusy(t *testing.T) {
	// Given: another connection owns the WAL writer while pairing reads.
	creator, locker := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	lock := holdClaimWriter(t, locker.DB)
	defer testutil.Rollback(t, lock)
	released := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		released <- lock.Rollback()
	}()
	defer func() {
		if err := <-released; err != nil {
			t.Error(err)
		}
	}()

	// When: pairing must restart its transaction after the writer releases.
	tuple := busyPairingTuple()
	pairing, err := creator.PrepareAgentPairing(t.Context(), tuple)
	if err != nil {
		t.Fatalf("prepare pairing while SQLite writer releases: %v", err)
	}

	// Then: exactly one complete tuple commits under the original identity.
	if pairing.AgentID != tuple.AgentID || pairing.State != "committing" {
		t.Fatalf("prepared pairing=%+v", pairing)
	}
	var count int
	if err := creator.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM agent_pairings WHERE agent_pin=?",
		tuple.AgentPin).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pairings=%d error=%v", count, err)
	}
}

func TestPrepareAgentPairingBusyHonorsCancellation(t *testing.T) {
	creator, locker := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	lock := holdClaimWriter(t, locker.DB)
	defer testutil.Rollback(t, lock)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	// When: the writer remains locked through the request deadline.
	tuple := busyPairingTuple()
	pairing, err := creator.PrepareAgentPairing(ctx, tuple)
	if err == nil || ctx.Err() != context.DeadlineExceeded ||
		pairing.AgentID != "" {
		t.Fatalf("pairing=%+v error=%v context=%v", pairing, err, ctx.Err())
	}

	// Then: no partial registration survives any failed attempt.
	var count int
	if err := creator.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM agents WHERE id=?", tuple.AgentID).
		Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial agents=%d error=%v", count, err)
	}
}

func busyPairingTuple() AgentPairingTuple {
	return AgentPairingTuple{
		AgentID: "pairing-busy", AgentName: "Pairing contention",
		Endpoint:            "https://pairing.invalid",
		PairingCodeHash:     bytes.Repeat([]byte{9}, 32),
		AgentPin:            strings.Repeat("d", 64),
		AgentPublicIdentity: "agent certificate",
		ServerPin:           strings.Repeat("e", 64),
		EncryptedIdentity:   "encrypted certificate",
		ServerPullEndpoint:  "https://server.invalid",
		Now:                 100, ExpiresAt: 500,
	}
}
