package agentproto

import (
	"errors"
	"testing"
	"time"
)

func TestPairingSession_PairAndCommit_isIdempotent_whenConfirmationRepeats(
	t *testing.T,
) {
	// Given
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	code := testPairingCode(t, 1)
	agentPin := testPin(t, "a")
	endpoint, err := ParsePullEndpoint("https://server.example.test/agent/v1")
	if err != nil {
		t.Fatalf("ParsePullEndpoint(): %v", err)
	}
	session := testPairingSession(t, PairingOffer{
		Code: code, AgentPin: agentPin, ExpiresAt: now.Add(time.Minute),
	})
	confirmation := PairingConfirmation{
		Code: code, ObservedAgent: agentPin, ServerPin: testPin(t, "b"),
		PullEndpoint: endpoint,
	}
	commits := 0
	if _, err := session.PairAndCommit(now, confirmation, func() error {
		commits++
		return nil
	}); err != nil {
		t.Fatalf("first PairAndCommit(): %v", err)
	}

	// When
	_, err = session.PairAndCommit(now, confirmation, func() error {
		commits++
		return nil
	})

	// Then
	if err != nil || commits != 1 || session.State() != PairingPaired {
		t.Fatalf(
			"repeat PairAndCommit() = %v, commits = %d, state = %q",
			err,
			commits,
			session.State(),
		)
	}
}

func TestPairingSession_ExpirePreventsQueuedCommit(t *testing.T) {
	// Given
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	code := testPairingCode(t, 1)
	agentPin := testPin(t, "a")
	endpoint, err := ParsePullEndpoint("https://server.example.test/agent/v1")
	if err != nil {
		t.Fatalf("ParsePullEndpoint(): %v", err)
	}
	session := testPairingSession(t, PairingOffer{
		Code: code, AgentPin: agentPin, ExpiresAt: now.Add(time.Minute),
	})
	confirmation := PairingConfirmation{
		Code: code, ObservedAgent: agentPin, ServerPin: testPin(t, "b"),
		PullEndpoint: endpoint,
	}

	// When
	if !session.Expire(now.Add(time.Minute)) {
		t.Fatal("Expire() = false, want true")
	}
	committed := false
	_, err = session.PairAndCommit(now, confirmation, func() error {
		committed = true
		return nil
	})

	// Then
	if !errors.Is(err, ErrPairingCodeExpired) || committed ||
		session.State() != PairingExpired {
		t.Fatalf(
			"PairAndCommit() = %v, committed = %t, state = %q",
			err,
			committed,
			session.State(),
		)
	}
}
