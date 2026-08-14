package mfa

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"durpdeploy/internal/db"
)

func TestWebAuthn_RegistrationAndAssertionOptions(t *testing.T) {
	// Given: one configured RP, a stable user handle, and a known credential.
	adapter, err := NewWebAuthnAdapter(WebAuthnConfig{
		Enabled: true,
		Origin:  "https://deploy.example.com",
		RPID:    "deploy.example.com",
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	if !bytes.Equal(
		[]byte(adapter.webauthn.Config.RPOrigins[0]),
		[]byte("https://deploy.example.com"),
	) {
		t.Fatal("adapter origin did not retain the configured value")
	}
	user := db.User{ID: 7, Email: "operator@example.com", Name: "Operator"}
	identity := db.WebauthnUser{
		UserID: user.ID, RpID: "deploy.example.com", UserHandle: bytes.Repeat([]byte{1}, 32),
	}
	credential := db.WebauthnCredential{
		CredentialID: []byte{1, 2}, UserID: user.ID, PublicKey: []byte{3},
		TransportsJson: `["usb"]`, Flags: int64(protocol.FlagUserPresent | protocol.FlagUserVerified),
	}

	// When: known-user registration and assertion ceremonies begin.
	registration, registrationSession, err := adapter.BeginRegistration(
		user, identity, []db.WebauthnCredential{credential},
	)
	if err != nil {
		t.Fatalf("begin registration: %v", err)
	}
	assertion, assertionSession, err := adapter.BeginAssertion(
		user, identity, []db.WebauthnCredential{credential},
	)
	if err != nil {
		t.Fatalf("begin assertion: %v", err)
	}

	// Then: RP, UV, exclusion/allow lists, and non-conditional mediation are exact.
	if registration.Response.RelyingParty.ID != "deploy.example.com" ||
		registrationSession.RelyingPartyID != "deploy.example.com" {
		t.Fatal("registration RP ID did not retain the configured value")
	}
	if registration.Response.Attestation != protocol.PreferNoAttestation {
		t.Fatal("registration attestation is not none")
	}
	if registration.Response.AuthenticatorSelection.ResidentKey !=
		protocol.ResidentKeyRequirementPreferred ||
		registration.Response.AuthenticatorSelection.UserVerification !=
			protocol.VerificationRequired {
		t.Fatal("registration does not prefer resident keys with required UV")
	}
	if registration.Mediation != protocol.MediationDefault ||
		assertion.Mediation != protocol.MediationDefault {
		t.Fatal("passkey-first or conditional mediation was enabled")
	}
	if len(registration.Response.CredentialExcludeList) != 1 ||
		len(assertion.Response.AllowedCredentials) != 1 {
		t.Fatal("known credential was not excluded/allowed")
	}
	if assertion.Response.RelyingPartyID != "deploy.example.com" ||
		assertionSession.RelyingPartyID != "deploy.example.com" ||
		assertion.Response.UserVerification != protocol.VerificationRequired ||
		assertionSession.UserVerification != protocol.VerificationRequired {
		t.Fatal("assertion did not bind configured RP with required UV")
	}
}

func TestWebAuthn_CredentialRoundTrip(t *testing.T) {
	// Given: every credential field currently represented by the generated model.
	row := db.WebauthnCredential{
		CredentialID:   []byte{1, 2, 3},
		UserID:         7,
		Name:           "hardware-key",
		PublicKey:      []byte{4, 5, 6},
		Aaguid:         bytes.Repeat([]byte{7}, 16),
		TransportsJson: `["usb","hybrid"]`,
		Flags: int64(protocol.FlagUserPresent | protocol.FlagUserVerified |
			protocol.FlagBackupEligible | protocol.FlagBackupState),
		SignCount:    42,
		CloneWarning: 1,
		Attachment: sql.NullString{
			String: string(protocol.CrossPlatform),
			Valid:  true,
		},
		AttestationType:               "none",
		AttestationFormat:             "none",
		AttestationClientDataJson:     []byte{8},
		AttestationClientDataHash:     []byte{9},
		AttestationAuthenticatorData:  []byte{10},
		AttestationPublicKeyAlgorithm: -7,
		AttestationObject:             []byte{11},
	}

	// When: the row is decoded and encoded for persistence.
	credential, err := CredentialFromDB(row)
	if err != nil {
		t.Fatalf("decode credential: %v", err)
	}
	stored, err := CredentialToDB(credential, row.UserID, row.Name)
	if err != nil {
		t.Fatalf("encode credential: %v", err)
	}

	// Then: protocol bytes, flags, counter, warning, transports, and attachment survive.
	if !bytes.Equal(stored.CredentialID, row.CredentialID) ||
		!bytes.Equal(stored.PublicKey, row.PublicKey) ||
		!bytes.Equal(stored.Aaguid, row.Aaguid) ||
		stored.TransportsJson != row.TransportsJson || stored.Flags != row.Flags ||
		stored.SignCount != row.SignCount || stored.CloneWarning != row.CloneWarning ||
		stored.Attachment != row.Attachment ||
		stored.AttestationType != row.AttestationType ||
		stored.AttestationFormat != row.AttestationFormat ||
		!bytes.Equal(
			stored.AttestationClientDataJson,
			row.AttestationClientDataJson,
		) ||
		!bytes.Equal(
			stored.AttestationClientDataHash,
			row.AttestationClientDataHash,
		) ||
		!bytes.Equal(
			stored.AttestationAuthenticatorData,
			row.AttestationAuthenticatorData,
		) ||
		stored.AttestationPublicKeyAlgorithm != row.AttestationPublicKeyAlgorithm ||
		!bytes.Equal(stored.AttestationObject, row.AttestationObject) {
		t.Fatal("credential metadata did not round-trip")
	}
}

func TestWebAuthn_RejectsInvalidIdentityAndCredentialData(t *testing.T) {
	// Given: a configured RP and valid user identity.
	adapter, err := NewWebAuthnAdapter(WebAuthnConfig{
		Enabled: true, Origin: "https://deploy.example.com", RPID: "deploy.example.com",
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	user := db.User{ID: 7, Email: "operator@example.com", Name: "Operator"}
	identity := db.WebauthnUser{
		UserID: user.ID, RpID: "deploy.example.com", UserHandle: bytes.Repeat([]byte{1}, 32),
	}

	// When: identity ownership, RP binding, and serialized credential metadata mutate.
	tests := []struct {
		name       string
		identity   db.WebauthnUser
		credential db.WebauthnCredential
	}{
		{
			"owner",
			db.WebauthnUser{
				UserID:     8,
				RpID:       identity.RpID,
				UserHandle: identity.UserHandle,
			},
			db.WebauthnCredential{},
		},
		{
			"RP ID",
			db.WebauthnUser{
				UserID:     user.ID,
				RpID:       "other.example.com",
				UserHandle: identity.UserHandle,
			},
			db.WebauthnCredential{},
		},
		{
			"handle",
			db.WebauthnUser{UserID: user.ID, RpID: identity.RpID},
			db.WebauthnCredential{},
		},
		{"transports", identity, db.WebauthnCredential{TransportsJson: "{"}},
		{
			"flags",
			identity,
			db.WebauthnCredential{TransportsJson: "[]", Flags: -1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := adapter.BeginAssertion(
				user,
				tt.identity,
				[]db.WebauthnCredential{tt.credential},
			)
			if err == nil {
				t.Fatal("mutated adapter input was accepted")
			}
		})
	}

	// Then: malformed identity or credential metadata never starts an assertion.
}

func TestWebAuthn_RejectsMutatedAssertionBinding(t *testing.T) {
	// Given: an issued known-user assertion binding.
	adapter, err := NewWebAuthnAdapter(WebAuthnConfig{
		Enabled: true, Origin: "https://deploy.example.com", RPID: "deploy.example.com",
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	user := db.User{ID: 7, Email: "operator@example.com", Name: "Operator"}
	identity := db.WebauthnUser{
		UserID: user.ID, RpID: "deploy.example.com", UserHandle: bytes.Repeat([]byte{1}, 32),
	}
	row := db.WebauthnCredential{
		CredentialID: []byte{1}, UserID: user.ID, PublicKey: []byte{2},
		TransportsJson: "[]", Flags: int64(protocol.FlagUserPresent | protocol.FlagUserVerified),
	}
	_, session, err := adapter.BeginAssertion(
		user,
		identity,
		[]db.WebauthnCredential{row},
	)
	if err != nil {
		t.Fatalf("begin assertion: %v", err)
	}

	// When: protected session fields or credential ownership are changed.
	tests := []struct {
		name   string
		mutate func(*webauthn.SessionData, *db.WebauthnCredential)
	}{
		{
			"RP ID",
			func(s *webauthn.SessionData, _ *db.WebauthnCredential) { s.RelyingPartyID = "other.example.com" },
		},
		{
			"challenge",
			func(s *webauthn.SessionData, _ *db.WebauthnCredential) { s.Challenge = "" },
		},
		{"UV", func(s *webauthn.SessionData, _ *db.WebauthnCredential) {
			s.UserVerification = protocol.VerificationPreferred
		}},
		{"allow list", func(s *webauthn.SessionData, _ *db.WebauthnCredential) {
			s.AllowedCredentialIDs = nil
		}},
		{
			"credential owner",
			func(_ *webauthn.SessionData, c *db.WebauthnCredential) { c.UserID++ },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSession := *session
			gotRow := row
			tt.mutate(&gotSession, &gotRow)
			err := adapter.ValidateAssertionBinding(AssertionBinding{
				User: user, Identity: identity, Rows: []db.WebauthnCredential{gotRow}, Session: gotSession,
			})
			if !errors.Is(err, ErrWebAuthnAssertionRejected) {
				t.Fatalf(
					"mutated binding error = %v, want generic rejection",
					err,
				)
			}
		})
	}

	// Then: no mismatch-specific protocol detail crosses the adapter boundary.
}

func TestWebAuthn_RejectsOriginAndRPIDMismatch(t *testing.T) {
	// Given: a URL not bound to the configured RP ID.
	config := WebAuthnConfig{
		Enabled: true, Origin: "https://other.example.com", RPID: "deploy.example.com",
	}

	// When: the adapter is created.
	_, err := NewWebAuthnAdapter(config)

	// Then: a mutated origin cannot expand the configured relying party.
	if !errors.Is(err, ErrWebAuthnIdentity) {
		t.Fatalf("origin/RP mismatch error = %v, want identity rejection", err)
	}
}

func TestWebAuthn_NewUserIdentityUsesRandomStableHandle(t *testing.T) {
	// Given: a user and a configured RP.
	const userID int64 = 7

	// When: a persistent WebAuthn identity is created.
	identity, err := NewWebAuthnUserIdentity(userID, "deploy.example.com")
	if err != nil {
		t.Fatalf("new identity: %v", err)
	}

	// Then: its random 32-byte handle is ready to persist unchanged for that user/RP.
	if identity.UserID != userID || identity.RpID != "deploy.example.com" ||
		len(identity.UserHandle) != 32 {
		t.Fatal("identity does not contain a stable persistence-ready handle")
	}
}

func TestWebAuthn_NewUserIdentityReadsExactlyThirtyTwoRandomBytes(
	t *testing.T,
) {
	// Given: deterministic entropy with more bytes than one user handle needs.
	entropy := bytes.Repeat([]byte{9}, 33)

	// When: the identity factory reads its random handle.
	identity, err := newWebAuthnUserIdentity(
		bytes.NewReader(entropy),
		7,
		"deploy.example.com",
	)

	// Then: it persists exactly the first 32 random bytes.
	if err != nil || !bytes.Equal(identity.UserHandle, entropy[:32]) {
		t.Fatal("identity did not use exactly 32 bytes of random entropy")
	}
}
