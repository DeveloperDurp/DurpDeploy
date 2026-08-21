package agentproto

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPairingSession_Pair_rejects_stale_pending_copy(t *testing.T) {
	// Given
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	code := testPairingCode(t, 1)
	agentPin := testPin(t, "a")
	confirmation := PairingConfirmation{
		Code:          code,
		ObservedAgent: agentPin,
		ServerPin:     testPin(t, "b"),
		PullEndpoint:  testPullEndpoint(t),
	}
	session := testPairingSession(t, PairingOffer{
		Code:      code,
		AgentPin:  agentPin,
		ExpiresAt: now.Add(time.Minute),
	})
	stale := session
	if _, err := session.Pair(now, confirmation); err != nil {
		t.Fatalf("first Pair(): %v", err)
	}

	// When
	_, err := stale.Pair(now, confirmation)

	// Then
	if !errors.Is(err, ErrPairingCodeUsed) {
		t.Fatalf(
			"stale Pair() error = %v, want %v",
			err,
			ErrPairingCodeUsed,
		)
	}
}

func TestDecodeRequest_rejects_duplicate_pair_field(t *testing.T) {
	// Given
	code, err := json.Marshal(testPairingCode(t, 1))
	if err != nil {
		t.Fatalf("marshal pairing code: %v", err)
	}
	agentPin := testPin(t, "a")
	serverPin := testPin(t, "b")
	body := fmt.Sprintf(
		`{"protocol":"agent/1","pairing_code":%s,`+
			`"server_pin":%q,"server_pin":%q,`+
			`"pull_endpoint":"https://server.example.test"}`,
		code,
		agentPin.String(),
		serverPin.String(),
	)

	// When
	_, err = DecodeRequest[PairRequest](strings.NewReader(body))

	// Then
	if !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf(
			"DecodeRequest() error = %v, want invalid JSON",
			err,
		)
	}
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || protocolErr.Reason != ReasonDuplicate {
		t.Fatalf(
			"DecodeRequest() error = %v, want duplicate protocol error",
			err,
		)
	}
}

func testPullEndpoint(t *testing.T) PullEndpoint {
	t.Helper()
	endpoint, err := ParsePullEndpoint("https://server.example.test")
	if err != nil {
		t.Fatalf("ParsePullEndpoint(): %v", err)
	}
	return endpoint
}
