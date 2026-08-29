package agentbootstrap_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"durpdeploy/internal/agentbootstrap"
)

func TestBootstrap_ExpiresPendingOfferButKeepsCommittedPairingAvailable(
	t *testing.T,
) {
	// Given
	pending := newServerInitListenerWithTTL(t, 500*time.Millisecond)
	committed := newServerInitListenerWithTTL(t, 500*time.Millisecond)
	serverIdentity := newServerInitIdentity(t)
	pullEndpoint := testServerInitEndpoint(t)

	if status := postServerInit(
		t,
		committed,
		serverIdentity,
		serverInitRequest(
			t,
			committed,
			committed.Offer().Code,
			serverIdentity,
			pullEndpoint,
		),
	); status != http.StatusNoContent {
		t.Fatalf("commit status = %d, want %d", status, http.StatusNoContent)
	}

	// When
	select {
	case <-pending.Done():
	case <-time.After(time.Second):
		t.Fatal("pending listener did not stop at offer expiry")
	}
	<-time.After(time.Second)

	acknowledgement := serverInitRequest(
		t,
		committed,
		committed.Offer().Code,
		serverIdentity,
		pullEndpoint,
	)
	acknowledgement.CompletionAck = true
	status := postServerInit(t, committed, serverIdentity, acknowledgement)

	// Then
	if status != http.StatusNoContent {
		t.Fatalf("acknowledgement status = %d, want %d", status, http.StatusNoContent)
	}
	select {
	case <-committed.Paired():
	default:
		t.Fatal("committed listener did not accept completion acknowledgement")
	}
}

func newServerInitListenerWithTTL(
	t *testing.T,
	ttl time.Duration,
) *agentbootstrap.Listener {
	t.Helper()
	listener, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: t.TempDir(), ListenAddr: "127.0.0.1:0", PairingTTL: ttl,
	})
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = listener.Shutdown(shutdownCtx)
	})
	return listener
}
