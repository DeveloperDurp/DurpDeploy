package handler_test

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func TestSecurity_ReauthPasskeyBeginPromotesBoundChallenge(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true, Origin: "https://example.org", RPID: "example.org",
	}})
	current := seedSession(t, h.repo, h.server, "admin")
	if _, err := h.repo.Queries.CreateWebAuthnUser(
		context.Background(),
		db.CreateWebAuthnUserParams{
			UserID: current.user.ID, RpID: "example.org",
			UserHandle: bytes.Repeat([]byte{1}, 32),
		},
	); err != nil {
		t.Fatalf("create passkey identity: %v", err)
	}
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(current.user.ID, []byte("reauth-key")),
	); err != nil {
		t.Fatalf("create passkey: %v", err)
	}

	// When
	password := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{"password": {"testpass"}},
		h.authHandler.SecurityReauthPost,
	)
	challenge := securityHiddenValues(t, password.Body.String())
	begin := postSecurityJSON(
		t,
		h,
		current,
		"/settings/security/reauth/webauthn/begin",
		"",
		h.authHandler.SecurityReauthWebAuthnBegin,
		challenge.Get("challenge_token"),
		challenge.Get("challenge_csrf"),
	)

	// Then
	if begin.Code != http.StatusOK ||
		begin.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(
			"passkey reauthentication begin did not return a no-store ceremony",
		)
	}
	var purpose string
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT purpose FROM mfa_challenges WHERE user_id = ?",
		current.user.ID,
	).Scan(&purpose); err != nil || purpose != string(mfa.ChallengePurposeWebAuthnAuth) {
		t.Fatalf(
			"passkey reauth challenge purpose = %q, error %v",
			purpose,
			err,
		)
	}
}

func TestSecurity_ReauthPasskeyFinishRefreshesSessionAndRecordsCloneWarning(
	t *testing.T,
) {
	// Given: a fresh session, a stored counter, and the existing signed UV assertion.
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
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		db.CreateWebAuthnCredentialParams{
			CredentialID:   credentialID,
			UserID:         current.user.ID,
			Name:           "test credential",
			PublicKey:      publicKey,
			TransportsJson: "[]",
			Flags: int64(
				protocol.FlagUserPresent | protocol.FlagUserVerified |
					protocol.FlagBackupEligible | protocol.FlagBackupState,
			),
			SignCount:         1,
			AttestationType:   "none",
			AttestationFormat: "none",
		},
	); err != nil {
		t.Fatalf("create passkey: %v", err)
	}
	challenges := mfa.NewChallengeService(
		mfa.ChallengeServiceConfig{Repository: h.repo},
	)
	pending, err := challenges.Issue(context.Background(), mfa.ChallengeIssue{
		UserID:    current.user.ID,
		SessionID: current.sessionToken,
		Purpose:   mfa.ChallengePurposeTOTPVerify,
	})
	if err != nil {
		t.Fatalf("issue reauthentication challenge: %v", err)
	}
	_, err = challenges.PromoteWebAuthn(
		context.Background(),
		mfa.ChallengeBinding{
			Token: pending.Token, CSRF: pending.CSRF, UserID: current.user.ID,
			SessionID: current.sessionToken,
			Purpose:   mfa.ChallengePurposeTOTPVerify,
		},
		webauthn.SessionData{
			RelyingPartyID:       "example.org",
			Challenge:            "7x3rpW3OSPZ0pEfM9juVmSWM6HZI5cOW8u8ModpGDjs",
			UserID:               handle,
			AllowedCredentialIDs: [][]byte{credentialID},
			UserVerification:     protocol.VerificationRequired,
		},
	)
	if err != nil {
		t.Fatalf("promote reauthentication challenge: %v", err)
	}
	if _, err := h.repo.DB.ExecContext(
		context.Background(),
		"UPDATE sessions SET reauthenticated_at = NULL WHERE id = ?",
		current.sessionToken,
	); err != nil {
		t.Fatalf("expire reauthentication freshness: %v", err)
	}
	before := readSession(t, h, current.sessionToken)
	if before.ReauthenticatedAt.Valid {
		t.Fatal("new session was unexpectedly reauthenticated")
	}

	// When: the handler receives the signed official assertion.
	finish := postSecurityJSON(
		t,
		h,
		current,
		"/settings/security/reauth/webauthn/finish",
		string(assertion),
		h.authHandler.SecurityReauthWebAuthnFinish,
		pending.Token,
		pending.CSRF,
	)

	// Then: the response is private, the current session is fresh, and the
	// counter anomaly is durable.
	if finish.Code != http.StatusSeeOther ||
		finish.Header().Get("Location") != "/settings/security" ||
		finish.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"passkey reauthentication response = %d %q %q",
			finish.Code,
			finish.Header().Get("Location"),
			finish.Header().Get("Cache-Control"),
		)
	}
	after := readSession(t, h, current.sessionToken)
	if !after.ReauthenticatedAt.Valid {
		t.Fatal("passkey reauthentication did not refresh the current session")
	}
	credential, err := h.repo.Queries.GetWebAuthnCredentialByID(
		context.Background(),
		credentialID,
	)
	if err != nil {
		t.Fatalf("load updated credential: %v", err)
	}
	if credential.SignCount != 1 || credential.CloneWarning != 1 {
		t.Fatalf(
			"credential counter state = (%d, %d), want (1, 1)",
			credential.SignCount,
			credential.CloneWarning,
		)
	}
}
