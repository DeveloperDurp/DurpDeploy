package agentserver

import (
	"context"
	"errors"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestPairingRevalidatesAfterAcknowledgementDeadline(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "registered", true: "deleted"}[deleted],
			func(t *testing.T) {
				testPairingExpiredAcknowledgement(t, deleted)
			},
		)
	}
}

func testPairingExpiredAcknowledgement(t *testing.T, deleted bool) {
	t.Helper()
	// Given: a durable pairing whose final acknowledgement reaches its deadline.
	f := newPairingFixture(t)
	result, err := f.pair(t)
	if err != nil {
		t.Fatal(err)
	}
	request := f.requests[len(f.requests)-1]
	f.post = func(ack agentproto.PairRequest) (int, error) {
		if deleted {
			if err := f.repo.DeleteAgent(t.Context(), ack.AgentID); err != nil {
				t.Fatal(err)
			}
		}
		return 0, context.DeadlineExceeded
	}
	ctx, cancel := context.WithDeadline(
		t.Context(),
		time.Now().Add(-time.Second),
	)
	defer cancel()

	// When: the completion request times out after activation committed.
	_, err = f.service.cleanup(
		ctx,
		&fakePairingConnection{fixture: f},
		request,
		result,
	)

	// Then: live registration determines success or conflict, not the expired ACK.
	if deleted {
		if !errors.Is(err, ErrPairingConflict) {
			t.Fatalf("deleted pairing error=%v, want conflict", err)
		}
	} else if err != nil {
		t.Fatalf("durable pairing error=%v, want success", err)
	}
}
