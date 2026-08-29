package agentbootstrap

import (
	"context"
	"testing"
	"time"
)

func TestListener_StopsExpiryTimerOnShutdown(t *testing.T) {
	listener, err := Start(Config{
		StateDir: t.TempDir(), ListenAddr: "127.0.0.1:0",
		PairingTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("Start(): %v", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := listener.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown(): %v", err)
	}
	select {
	case <-listener.expiryDone:
	case <-time.After(time.Second):
		t.Fatal("expiry timer remained after listener shutdown")
	}
}
