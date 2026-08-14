package mfa

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"durpdeploy/internal/db"
)

func TestWebAuthn_FinishRegistrationAcceptsOfficialPackedSelfES256Vector(
	t *testing.T,
) {
	// Given: the v0.17.4 fixed Packed Self ES256 registration vector and a bound session.
	vector := webauthnRegistrationPackedSelfES256(t)
	adapter, user, identity, _ := webauthnVectorFixture(t)
	session := webauthn.SessionData{
		RelyingPartyID:   "example.org",
		Challenge:        vector.challenge,
		UserID:           identity.UserHandle,
		UserVerification: protocol.VerificationRequired,
		CredParams: []protocol.CredentialParameter{{
			Type: protocol.PublicKeyCredentialType, Algorithm: -7,
		}},
	}

	// When: the adapter finishes registration with the signed fixed vector.
	credential, err := adapter.FinishRegistration(RegistrationBinding{
		User: user, Identity: identity, Session: session,
		Request: httptest.NewRequest(
			http.MethodPost,
			"/",
			bytes.NewReader(vector.body),
		),
	})

	// Then: the official vector produces the expected credential.
	if err != nil {
		t.Fatalf("finish registration: %v", err)
	}
	if !bytes.Equal(credential.ID, vector.credentialID) {
		t.Fatal("registration credential did not match the official vector")
	}
}

func TestWebAuthn_FinishMethodsRejectMalformedResponses(t *testing.T) {
	// Given: a known-user registration and assertion session.
	adapter, user, identity, row := webauthnTestFixture(t)
	_, registrationSession, err := adapter.BeginRegistration(
		user,
		identity,
		nil,
	)
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	_, assertionSession, err := adapter.BeginAssertion(
		user,
		identity,
		[]db.WebauthnCredential{row},
	)
	if err != nil {
		t.Fatalf("begin assertion: %v", err)
	}

	// When: malformed protocol bodies are passed through the actual finish APIs.
	registrationRequest := httptest.NewRequest(
		http.MethodPost,
		"/",
		strings.NewReader("{"),
	)
	assertionRequest := httptest.NewRequest(
		http.MethodPost,
		"/",
		strings.NewReader("{"),
	)
	_, registrationErr := adapter.FinishRegistration(RegistrationBinding{
		User: user, Identity: identity, Session: *registrationSession, Request: registrationRequest,
	})
	_, assertionErr := adapter.FinishAssertion(AssertionBinding{
		User: user, Identity: identity, Rows: []db.WebauthnCredential{row}, Session: *assertionSession,
		Request: assertionRequest,
	})

	// Then: malformed credential IDs, origins, handles, or counters cannot leak detail.
	if !errors.Is(registrationErr, ErrWebAuthnRegistrationRejected) ||
		!errors.Is(assertionErr, ErrWebAuthnAssertionRejected) {
		t.Fatal("malformed finish response was not generically rejected")
	}
}

func webauthnTestFixture(t *testing.T) (
	*WebAuthnAdapter,
	db.User,
	db.WebauthnUser,
	db.WebauthnCredential,
) {
	t.Helper()
	adapter, err := NewWebAuthnAdapter(WebAuthnConfig{
		Enabled: true,
		Origin:  "https://deploy.example.com",
		RPID:    "deploy.example.com",
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	user := db.User{ID: 7, Email: "operator@example.com", Name: "Operator"}
	identity := db.WebauthnUser{
		UserID:     user.ID,
		RpID:       "deploy.example.com",
		UserHandle: bytes.Repeat([]byte{1}, 32),
	}
	row := db.WebauthnCredential{
		CredentialID:   []byte{1},
		UserID:         user.ID,
		PublicKey:      []byte{2},
		TransportsJson: "[]",
		Flags: int64(
			protocol.FlagUserPresent | protocol.FlagUserVerified,
		),
	}
	return adapter, user, identity, row
}

func webauthnVectorFixture(t *testing.T) (
	*WebAuthnAdapter,
	db.User,
	db.WebauthnUser,
	db.WebauthnCredential,
) {
	t.Helper()
	return webauthnVectorFixtureFor(t, webauthnAssertionNoneES256(t))
}

func webauthnVectorFixtureFor(t *testing.T, vector webauthnAssertionVector) (
	*WebAuthnAdapter,
	db.User,
	db.WebauthnUser,
	db.WebauthnCredential,
) {
	t.Helper()
	adapter, err := NewWebAuthnAdapter(WebAuthnConfig{
		Enabled: true,
		Origin:  "https://example.org",
		RPID:    "example.org",
	})
	if err != nil {
		t.Fatalf("new vector adapter: %v", err)
	}
	user := db.User{ID: 7, Email: "operator@example.com", Name: "Operator"}
	identity := db.WebauthnUser{
		UserID: user.ID, RpID: "example.org", UserHandle: bytes.Repeat([]byte{1}, 32),
	}
	row := db.WebauthnCredential{
		CredentialID:   vector.credentialID,
		UserID:         user.ID,
		PublicKey:      vector.publicKey,
		TransportsJson: "[]",
		Flags: int64(protocol.FlagUserPresent | protocol.FlagUserVerified |
			protocol.FlagBackupEligible | protocol.FlagBackupState),
	}
	return adapter, user, identity, row
}
