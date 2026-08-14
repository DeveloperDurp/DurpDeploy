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

func TestSecurity_PasskeyVerificationActivatesOnlyAfterValidAssertion(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true, Origin: "https://example.org", RPID: "example.org",
	}})
	current := seedSession(t, h.repo, h.server, "admin")
	credentialID, publicKey, assertion := officialUVAssertion(t)
	handle := bytes.Repeat([]byte{1}, 32)
	if _, err := h.repo.Queries.CreateWebAuthnUser(
		context.Background(),
		db.CreateWebAuthnUserParams{
			UserID: current.user.ID, RpID: "example.org", UserHandle: handle,
		},
	); err != nil {
		t.Fatalf("create passkey identity: %v", err)
	}
	ceremony, err := json.Marshal(passkeyVerificationCeremony{
		Credential: db.CreateWebAuthnCredentialParams{
			CredentialID:   credentialID,
			UserID:         current.user.ID,
			Name:           "staged passkey",
			PublicKey:      publicKey,
			TransportsJson: "[]",
			Flags: int64(protocol.FlagUserPresent | protocol.FlagUserVerified |
				protocol.FlagBackupEligible | protocol.FlagBackupState),
			AttestationType:   "none",
			AttestationFormat: "none",
		},
		Session: webauthn.SessionData{
			RelyingPartyID:       "example.org",
			Challenge:            "7x3rpW3OSPZ0pEfM9juVmSWM6HZI5cOW8u8ModpGDjs",
			UserID:               handle,
			AllowedCredentialIDs: [][]byte{credentialID},
			UserVerification:     protocol.VerificationRequired,
		},
	})
	if err != nil {
		t.Fatalf("marshal staged passkey: %v", err)
	}
	pending, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{Repository: h.repo}).
		Issue(
			context.Background(),
			mfa.ChallengeIssue{
				UserID:       current.user.ID,
				SessionID:    current.sessionToken,
				Purpose:      mfa.ChallengePurposeWebAuthnAuth,
				CeremonyJSON: string(ceremony),
			},
		)
	if err != nil {
		t.Fatalf("issue staged passkey verification: %v", err)
	}

	// When
	invalid := postSecurityJSONHTTP(
		t,
		current,
		h.server,
		"/settings/security/passkeys/test/finish",
		[]byte(`{}`),
		pending.Token,
		pending.CSRF,
	)
	invalid.Body.Close()
	success := postSecurityJSONHTTP(
		t,
		current,
		h.server,
		"/settings/security/passkeys/test/finish",
		assertion,
		pending.Token,
		pending.CSRF,
	)
	defer success.Body.Close()
	var payload struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.NewDecoder(success.Body).Decode(&payload); err != nil {
		t.Fatalf("decode successful passkey verification: %v", err)
	}
	replay := postSecurityJSONHTTP(
		t,
		current,
		h.server,
		"/settings/security/passkeys/test/finish",
		assertion,
		pending.Token,
		pending.CSRF,
	)
	defer replay.Body.Close()

	// Then
	if invalid.StatusCode != http.StatusUnprocessableEntity ||
		success.StatusCode != http.StatusOK || replay.StatusCode != http.StatusSeeOther ||
		replay.Header.Get("Location") != "/login" {
		t.Fatalf(
			"passkey verification statuses = %d, %d, %d",
			invalid.StatusCode,
			success.StatusCode,
			replay.StatusCode,
		)
	}
	if len(payload.RecoveryCodes) != 10 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?",
			current.user.ID,
		) != 1 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
			current.user.ID,
		) != 10 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
			current.user.ID,
		) != 0 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
			current.user.ID,
		) != 0 {
		t.Fatal(
			"passkey test did not atomically commit only the verified staged factor",
		)
	}
}

type passkeyVerificationCeremony struct {
	Credential db.CreateWebAuthnCredentialParams `json:"credential"`
	Session    webauthn.SessionData              `json:"session"`
}

func postSecurityJSONHTTP(
	t *testing.T,
	current *authedSession,
	serverURL, path string,
	body []byte,
	token, challengeCSRF string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		serverURL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("new request for %s: %v", path, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", current.csrfToken)
	request.Header.Set("X-MFA-Challenge", token)
	request.Header.Set("X-MFA-Challenge-CSRF", challengeCSRF)
	response, err := current.client.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return response
}
