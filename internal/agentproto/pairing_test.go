package agentproto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPairingSession_Pair_binds_mutual_pins_when_confirmation_matches(
	t *testing.T,
) {
	// Given
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	code := testPairingCode(t, 1)
	agentPin := testPin(t, "a")
	serverPin := testPin(t, "b")
	endpoint, err := ParsePullEndpoint("https://server.example.test/agent/v1")
	if err != nil {
		t.Fatalf("ParsePullEndpoint(): %v", err)
	}
	session, err := NewPairingSession(PairingOffer{
		Code:      code,
		AgentPin:  agentPin,
		ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("NewPairingSession(): %v", err)
	}

	// When
	paired, err := session.Pair(now, PairingConfirmation{
		Code:          code,
		ObservedAgent: agentPin,
		ServerPin:     serverPin,
		PullEndpoint:  endpoint,
	})

	// Then
	if err != nil {
		t.Fatalf("Pair(): %v", err)
	}
	if paired.State() != PairingPaired {
		t.Fatalf("State() = %q, want %q", paired.State(), PairingPaired)
	}
	trust, ok := paired.Trust()
	if !ok || trust.Agent != agentPin || trust.Server != serverPin {
		t.Fatalf("Trust() = %#v, %t; want mutual pins", trust, ok)
	}
}

func TestPairingSession_Pair_rejects_wrong_agent_fingerprint(t *testing.T) {
	// Given
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	code := testPairingCode(t, 1)
	session := testPairingSession(t, PairingOffer{
		Code:      code,
		AgentPin:  testPin(t, "a"),
		ExpiresAt: now.Add(time.Minute),
	})
	endpoint, err := ParsePullEndpoint("https://server.example.test/agent/v1")
	if err != nil {
		t.Fatalf("ParsePullEndpoint(): %v", err)
	}

	// When
	_, err = session.Pair(now, PairingConfirmation{
		Code:          code,
		ObservedAgent: testPin(t, "c"),
		ServerPin:     testPin(t, "b"),
		PullEndpoint:  endpoint,
	})

	// Then
	if !errors.Is(err, ErrAgentFingerprintMismatch) {
		t.Fatalf("Pair() error = %v, want %v", err, ErrAgentFingerprintMismatch)
	}
}

func TestPairingSession_Pair_rejects_expired_code(t *testing.T) {
	// Given
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	code := testPairingCode(t, 1)
	session := testPairingSession(t, PairingOffer{
		Code:      code,
		AgentPin:  testPin(t, "a"),
		ExpiresAt: now,
	})
	endpoint, err := ParsePullEndpoint("https://server.example.test/agent/v1")
	if err != nil {
		t.Fatalf("ParsePullEndpoint(): %v", err)
	}

	// When
	expired, err := session.Pair(now, PairingConfirmation{
		Code:          code,
		ObservedAgent: testPin(t, "a"),
		ServerPin:     testPin(t, "b"),
		PullEndpoint:  endpoint,
	})

	// Then
	if !errors.Is(err, ErrPairingCodeExpired) {
		t.Fatalf("Pair() error = %v, want %v", err, ErrPairingCodeExpired)
	}
	if expired.State() != PairingExpired {
		t.Fatalf("State() = %q, want %q", expired.State(), PairingExpired)
	}
}

func TestPairingSession_Pair_rejects_reused_code(t *testing.T) {
	// Given
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	code := testPairingCode(t, 1)
	session := testPairingSession(t, PairingOffer{
		Code:      code,
		AgentPin:  testPin(t, "a"),
		ExpiresAt: now.Add(time.Minute),
	})
	endpoint, err := ParsePullEndpoint("https://server.example.test/agent/v1")
	if err != nil {
		t.Fatalf("ParsePullEndpoint(): %v", err)
	}
	paired, err := session.Pair(now, PairingConfirmation{
		Code:          code,
		ObservedAgent: testPin(t, "a"),
		ServerPin:     testPin(t, "b"),
		PullEndpoint:  endpoint,
	})
	if err != nil {
		t.Fatalf("first Pair(): %v", err)
	}

	// When
	_, err = paired.Pair(now, PairingConfirmation{
		Code:          code,
		ObservedAgent: testPin(t, "a"),
		ServerPin:     testPin(t, "b"),
		PullEndpoint:  endpoint,
	})

	// Then
	if !errors.Is(err, ErrPairingCodeUsed) {
		t.Fatalf("second Pair() error = %v, want %v", err, ErrPairingCodeUsed)
	}
}

func TestPairingSession_PairAndCommit_keepsCodePending_when_persistenceFails(
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
		Code:          code,
		ObservedAgent: agentPin,
		ServerPin:     testPin(t, "b"),
		PullEndpoint:  endpoint,
	}
	persistErr := errors.New("state disk is unavailable")

	// When
	_, err = session.PairAndCommit(now, confirmation, func() error {
		return persistErr
	})

	// Then
	if !errors.Is(err, persistErr) {
		t.Fatalf("pair error = %v, want persistence error", err)
	}
	if state := session.State(); state != PairingPending {
		t.Fatalf("session state = %q, want pending", state)
	}
}

func TestPairRequest_Decode_rejects_unknown_fields_and_unsupported_versions(
	t *testing.T,
) {
	code := testPairingCode(t, 1)
	encodedCode, err := json.Marshal(code)
	if err != nil {
		t.Fatalf("marshal pairing code: %v", err)
	}
	pin := testPin(t, "a")

	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{
			name: "unknown field",
			body: fmt.Sprintf(
				`{"protocol":"agent/1","pairing_code":%s,"server_pin":"%s","pull_endpoint":"https://server.example.test/agent/v1","extra":true}`,
				encodedCode,
				pin,
			),
			wantErr: ErrUnknownField,
		},
		{
			name: "unsupported version",
			body: fmt.Sprintf(
				`{"protocol":"agent/2","pairing_code":%s,"server_pin":"%s","pull_endpoint":"https://server.example.test/agent/v1"}`,
				encodedCode,
				pin,
			),
			wantErr: ErrUnsupportedProtocol,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			body := strings.NewReader(test.body)

			// When
			_, err := DecodeRequest[PairRequest](body)

			// Then
			if !errors.Is(err, test.wantErr) {
				t.Fatalf(
					"DecodeRequest() error = %v, want %v",
					err,
					test.wantErr,
				)
			}
		})
	}
}

func TestPairingCode_Parse_rejects_insufficient_entropy(t *testing.T) {
	// Given
	raw := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31))

	// When
	_, err := ParsePairingCode(raw)

	// Then
	if !errors.Is(err, ErrInvalidPairingCode) {
		t.Fatalf(
			"ParsePairingCode() error = %v, want %v",
			err,
			ErrInvalidPairingCode,
		)
	}
}

func testPairingSession(t *testing.T, offer PairingOffer) PairingSession {
	t.Helper()
	session, err := NewPairingSession(offer)
	if err != nil {
		t.Fatalf("NewPairingSession(): %v", err)
	}
	return session
}

func testPairingCode(t *testing.T, byteValue byte) PairingCode {
	t.Helper()
	raw := base64.RawURLEncoding.EncodeToString(
		bytes.Repeat([]byte{byteValue}, PairingCodeEntropyBytes),
	)
	code, err := ParsePairingCode(raw)
	if err != nil {
		t.Fatalf("ParsePairingCode(): %v", err)
	}
	return code
}

func testPin(t *testing.T, digit string) SHA256Pin {
	t.Helper()
	pin, err := ParseSHA256Pin(strings.Repeat(digit, 64))
	if err != nil {
		t.Fatalf("ParseSHA256Pin(): %v", err)
	}
	return pin
}
