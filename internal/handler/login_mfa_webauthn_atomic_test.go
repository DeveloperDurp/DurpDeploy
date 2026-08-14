package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type webauthnLoginFixture struct {
	pending   pendingLogin
	assertion []byte
}

func TestLogin_WebAuthnDoesNotAdvanceWhenFinalSessionCreationFails(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	fixture := prepareWebAuthnLogin(t, h)
	blockFinalSessionCreation(t, h)
	recordWebAuthnCounterUpdates(t, h)

	// When
	response := finishWebAuthnLogin(t, h, fixture)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("WebAuthn response = %d, want 422", response.StatusCode)
	}
	if webauthnCounterUpdates(t, h) != 0 {
		t.Fatal("failed final session creation advanced the WebAuthn counter")
	}
}

func prepareWebAuthnLogin(
	t *testing.T,
	h *authHarness,
) webauthnLoginFixture {
	t.Helper()
	configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true, Origin: "https://example.org", RPID: "example.org",
	}})
	user := h.seedUser(t, "passkey-atomic@example.com", "hunter2")
	credentialID, publicKey, assertion := officialUVAssertion(t)
	handle := bytes.Repeat([]byte{1}, 32)
	if _, err := h.repo.Queries.CreateWebAuthnUser(
		context.Background(),
		db.CreateWebAuthnUserParams{
			UserID: user.ID, RpID: "example.org", UserHandle: handle,
		},
	); err != nil {
		t.Fatalf("create WebAuthn identity: %v", err)
	}
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		db.CreateWebAuthnCredentialParams{
			CredentialID:   credentialID,
			UserID:         user.ID,
			Name:           "atomic credential",
			PublicKey:      publicKey,
			TransportsJson: "[]",
			Flags: int64(
				protocol.FlagUserPresent |
					protocol.FlagUserVerified |
					protocol.FlagBackupEligible |
					protocol.FlagBackupState,
			),
			AttestationType:   "none",
			AttestationFormat: "none",
		},
	); err != nil {
		t.Fatalf("create WebAuthn credential: %v", err)
	}
	pending := loginPending(t, h, user.Email)
	begin, err := http.NewRequest(
		http.MethodPost,
		h.server+"/login/mfa/webauthn/begin",
		nil,
	)
	if err != nil {
		t.Fatalf("new assertion begin request: %v", err)
	}
	begin.Header.Set("X-CSRF-Token", pending.csrf)
	beginResponse, err := pending.client.Do(begin)
	if err != nil {
		t.Fatalf("begin assertion: %v", err)
	}
	beginResponse.Body.Close()
	if beginResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"begin assertion response = %d, want 200",
			beginResponse.StatusCode,
		)
	}
	session := webauthn.SessionData{
		RelyingPartyID:       "example.org",
		Challenge:            "7x3rpW3OSPZ0pEfM9juVmSWM6HZI5cOW8u8ModpGDjs",
		UserID:               handle,
		AllowedCredentialIDs: [][]byte{credentialID},
		UserVerification:     protocol.VerificationRequired,
	}
	ceremony, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal assertion ceremony: %v", err)
	}
	if _, err := h.repo.DB.ExecContext(
		context.Background(),
		"UPDATE mfa_challenges SET ceremony_json = ? WHERE user_id = ?",
		string(ceremony),
		user.ID,
	); err != nil {
		t.Fatalf("replace assertion ceremony: %v", err)
	}
	return webauthnLoginFixture{pending: pending, assertion: assertion}
}

func finishWebAuthnLogin(
	t *testing.T,
	h *authHarness,
	fixture webauthnLoginFixture,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		h.server+"/login/mfa/webauthn/finish",
		bytes.NewReader(fixture.assertion),
	)
	if err != nil {
		t.Fatalf("new assertion finish request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", fixture.pending.csrf)
	response, err := fixture.pending.client.Do(request)
	if err != nil {
		t.Fatalf("finish assertion: %v", err)
	}
	return response
}

func recordWebAuthnCounterUpdates(t *testing.T, h *authHarness) {
	t.Helper()
	_, err := h.repo.DB.ExecContext(context.Background(), `
		CREATE TABLE webauthn_counter_updates (id INTEGER PRIMARY KEY);
		CREATE TRIGGER record_webauthn_counter_update
		AFTER UPDATE ON webauthn_credentials
		BEGIN
			INSERT INTO webauthn_counter_updates(id) VALUES (NULL);
		END`)
	if err != nil {
		t.Fatalf("record WebAuthn counter updates: %v", err)
	}
}

func webauthnCounterUpdates(t *testing.T, h *authHarness) int {
	t.Helper()
	var updates int
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM webauthn_counter_updates",
	).Scan(&updates); err != nil {
		t.Fatalf("count WebAuthn counter updates: %v", err)
	}
	return updates
}
