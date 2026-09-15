package agentserver

import (
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestPairingCrashCheckpointRecovery(t *testing.T) {
	t.Run("CrashBeforeFirstRequest", func(t *testing.T) {
		fixture := newPairingFixture(t)
		fixture.service.afterPersist = func() error { return errTestInterruption }
		if _, err := fixture.pair(t); err == nil || len(fixture.requests) != 0 {
			t.Fatalf("err=%v requests=%d", err, len(fixture.requests))
		}
		fixture.service.afterPersist = nil
		if _, err := fixture.pair(t); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("LostFirstPhaseResponse", func(t *testing.T) {
		fixture := newPairingFixture(t)
		lost := true
		fixture.post = func(request agentproto.PairRequest) (int, error) {
			if !request.CompletionAck && lost {
				lost = false
				return 0, errTestInterruption
			}
			return 204, nil
		}
		if _, err := fixture.pair(t); err == nil {
			t.Fatal("lost response reported success")
		}
		if _, err := fixture.pair(t); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("CrashBeforeServerActivation", func(t *testing.T) {
		fixture := newPairingFixture(t)
		fixture.service.afterFirstPhase = func() error { return errTestInterruption }
		if _, err := fixture.pair(t); err == nil {
			t.Fatal("interruption reported success")
		}
		fixture.service.afterFirstPhase = nil
		if _, err := fixture.pair(t); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("CrashBeforeCleanupAcknowledgement", func(t *testing.T) {
		fixture := newPairingFixture(t)
		fixture.service.afterActivation = func() error { return errTestInterruption }
		if _, err := fixture.pair(t); err == nil {
			t.Fatal("interruption reported success")
		}
		fixture.service.afterActivation = nil
		if _, err := fixture.pair(t); err != nil {
			t.Fatal(err)
		}
		if got := fixture.requests[len(fixture.requests)-1]; !got.CompletionAck {
			t.Fatal("recovery repeated first phase")
		}
	})

	t.Run("LostCleanupResponse", func(t *testing.T) {
		fixture := newPairingFixture(t)
		lost := true
		fixture.post = func(request agentproto.PairRequest) (int, error) {
			if request.CompletionAck && lost {
				lost = false
				return 0, errTestInterruption
			}
			return 204, nil
		}
		if _, err := fixture.pair(t); err == nil {
			t.Fatal("lost cleanup response reported success")
		}
		if _, err := fixture.pair(t); err != nil {
			t.Fatal(err)
		}
	})
}
