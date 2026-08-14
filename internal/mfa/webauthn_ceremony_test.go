package mfa

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"durpdeploy/internal/db"
)

func TestWebAuthn_OfficialVectorSourceUsesConfiguredModuleCache(t *testing.T) {
	// Given: a module cache location distinct from the developer default.
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)

	// When: the fixture source path is resolved.
	path := webauthnOfficialVectorSourcePath(t)

	// Then: it uses the configured cache rather than a machine-specific path.
	want := filepath.Join(
		cache,
		"github.com/go-webauthn/webauthn@v0.17.4/protocol/specification_vectors_e2e_test.go",
	)
	if path != want {
		t.Fatal("official WebAuthn vector path ignored GOMODCACHE")
	}
}

func TestWebAuthn_FinishAssertionAcceptsOfficialUVLongCredentialIDVector(
	t *testing.T,
) {
	// Given: the v0.17.4 None ES256 long-credential-ID UV assertion vector.
	vector := webauthnAssertionNoneES256LongCredentialID(t)
	adapter, user, identity, row := webauthnVectorFixtureFor(t, vector)

	// When: the adapter finishes the known-user assertion with UV required.
	credential, err := adapter.FinishAssertion(AssertionBinding{
		User: user, Identity: identity, Rows: []db.WebauthnCredential{row},
		Session: webauthnVectorAssertionSession(vector, identity.UserHandle),
		Request: httptest.NewRequest(
			http.MethodPost,
			"/",
			bytes.NewReader(vector.body),
		),
	})

	// Then: the valid signed vector is accepted without weakening UV.
	if err != nil {
		t.Fatalf("finish assertion: %v", err)
	}
	if !bytes.Equal(credential.ID, vector.credentialID) {
		t.Fatal("assertion credential did not match the official vector")
	}
}

func TestWebAuthn_OfficialUVLongCredentialIDUsesSpecificationVector(
	t *testing.T,
) {
	// Given: the v0.17.4 None ES256 long-credential-ID assertion fixture.
	vector := webauthnAssertionNoneES256LongCredentialID(t)

	// When: its lookup ID is decoded.
	usesSpecificationID := bytes.HasPrefix(
		vector.credentialID,
		[]byte{0x3a, 0x76, 0x1a, 0x4e},
	)

	// Then: it starts with the official vector rather than a local substitute.
	if !usesSpecificationID {
		t.Fatal("long credential ID is not the official specification vector")
	}
}

func TestWebAuthn_FinishAssertionRejectsOfficialNoneES256VectorWithoutUV(
	t *testing.T,
) {
	// Given: the v0.17.4 fixed None ES256 assertion vector and a required-UV binding.
	vector := webauthnAssertionNoneES256(t)
	adapter, user, identity, row := webauthnVectorFixture(t)

	// When: the adapter finishes the known-user assertion with the signed vector.
	_, err := adapter.FinishAssertion(AssertionBinding{
		User: user, Identity: identity, Rows: []db.WebauthnCredential{row},
		Session: webauthnVectorAssertionSession(vector, identity.UserHandle),
		Request: httptest.NewRequest(
			http.MethodPost,
			"/",
			bytes.NewReader(vector.body),
		),
	})

	// Then: it cannot bypass the required UV policy.
	if !errors.Is(err, ErrWebAuthnAssertionRejected) {
		t.Fatalf("finish assertion error = %v, want generic rejection", err)
	}
}

func TestWebAuthn_FinishAssertionRejectsMutatedOfficialBinding(t *testing.T) {
	// Given: a valid known-user assertion binding based on the official vector.
	vector := webauthnAssertionNoneES256LongCredentialID(t)
	adapter, user, identity, row := webauthnVectorFixtureFor(t, vector)
	validSession := webauthnVectorAssertionSession(vector, identity.UserHandle)

	// When: binding fields are changed before the adapter finishes the assertion.
	tests := []struct {
		name   string
		mutate func(*webauthn.SessionData, *db.WebauthnCredential)
	}{
		{
			"stale challenge",
			func(s *webauthn.SessionData, _ *db.WebauthnCredential) {
				s.Challenge = "stale"
			},
		},
		{
			"UV requirement",
			func(s *webauthn.SessionData, _ *db.WebauthnCredential) {
				s.UserVerification = protocol.VerificationPreferred
			},
		},
		{"allow list", func(s *webauthn.SessionData, _ *db.WebauthnCredential) {
			s.AllowedCredentialIDs = [][]byte{[]byte("changed")}
		}},
		{
			"credential ID",
			func(_ *webauthn.SessionData, c *db.WebauthnCredential) {
				c.CredentialID = []byte("changed")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := validSession
			credential := row
			tt.mutate(&session, &credential)

			_, err := adapter.FinishAssertion(AssertionBinding{
				User: user, Identity: identity, Rows: []db.WebauthnCredential{credential},
				Session: session,
				Request: httptest.NewRequest(
					http.MethodPost,
					"/",
					bytes.NewReader(vector.body),
				),
			})
			if !errors.Is(err, ErrWebAuthnAssertionRejected) {
				t.Fatalf(
					"mutated assertion error = %v, want generic rejection",
					err,
				)
			}
		})
	}

	// Then: changed ceremony state is never accepted or exposed.
}

func TestWebAuthn_UpstreamValidatorRejectsSignedUVMutation(t *testing.T) {
	// Given: a valid parsed official UV assertion and matching credential state.
	vector := webauthnAssertionNoneES256LongCredentialID(t)
	adapter, user, identity, row := webauthnVectorFixtureFor(t, vector)
	mapped, err := webauthnUserFromDB(
		user,
		identity,
		[]db.WebauthnCredential{row},
		"example.org",
	)
	if err != nil {
		t.Fatalf("map vector credential: %v", err)
	}

	// When: the signed authenticator flags are changed without a re-signature.
	vector.parsed.Response.AuthenticatorData.Flags &^= protocol.FlagUserVerified
	_, err = adapter.webauthn.ValidateLogin(
		mapped,
		webauthnVectorAssertionSession(vector, identity.UserHandle),
		vector.parsed,
	)

	// Then: the upstream validation seam rejects the changed signed response.
	if err == nil {
		t.Fatal("signed UV mutation was accepted")
	}
}

func TestWebAuthn_CounterMappingPersistsIncreaseAndCloneWarnings(t *testing.T) {
	// Given: the credential returned by a successful required-UV assertion.
	vector := webauthnAssertionNoneES256LongCredentialID(t)
	adapter, user, identity, row := webauthnVectorFixtureFor(t, vector)
	credential, err := adapter.FinishAssertion(AssertionBinding{
		User: user, Identity: identity, Rows: []db.WebauthnCredential{row},
		Session: webauthnVectorAssertionSession(vector, identity.UserHandle),
		Request: httptest.NewRequest(
			http.MethodPost,
			"/",
			bytes.NewReader(vector.body),
		),
	})
	if err != nil {
		t.Fatalf("finish assertion: %v", err)
	}

	// When: the upstream authenticator counter increases, equals, and decreases.
	credential.Authenticator.UpdateCounter(1)
	increased, err := CredentialToDB(credential, user.ID, "vector")
	if err != nil {
		t.Fatalf("persist increased counter: %v", err)
	}
	credential.Authenticator.UpdateCounter(1)
	equal, err := CredentialToDB(credential, user.ID, "vector")
	if err != nil {
		t.Fatalf("persist equal counter: %v", err)
	}
	credential.Authenticator.UpdateCounter(0)
	decreased, err := CredentialToDB(credential, user.ID, "vector")
	if err != nil {
		t.Fatalf("persist decreased counter: %v", err)
	}

	// Then: increases advance, while equal/decreased values retain a clone warning.
	if increased.SignCount != 1 || increased.CloneWarning != 0 ||
		equal.SignCount != 1 || equal.CloneWarning != 1 ||
		decreased.SignCount != 1 || decreased.CloneWarning != 1 {
		t.Fatal(
			"counter or clone-warning persistence did not match validation semantics",
		)
	}
}

func webauthnVectorAssertionSession(
	vector webauthnAssertionVector,
	userHandle []byte,
) webauthn.SessionData {
	return webauthn.SessionData{
		RelyingPartyID:       "example.org",
		Challenge:            vector.challenge,
		UserID:               userHandle,
		AllowedCredentialIDs: [][]byte{vector.credentialID},
		UserVerification:     protocol.VerificationRequired,
	}
}
