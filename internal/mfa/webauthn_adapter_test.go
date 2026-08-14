package mfa

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"durpdeploy/internal/db"
)

func TestWebAuthn_FinishAssertionMapsMutatedOfficialResponses(t *testing.T) {
	// Given: the official UV assertion and a bound known-user session.
	vector := webauthnAssertionNoneES256LongCredentialID(t)
	tests := []struct {
		name   string
		mutate func(*WebAuthnAdapter, *webauthn.SessionData, *webauthnAssertionResponseFields)
	}{
		{
			"challenge",
			func(_ *WebAuthnAdapter, session *webauthn.SessionData, _ *webauthnAssertionResponseFields) {
				session.Challenge = "stale"
			},
		},
		{
			"origin",
			func(adapter *WebAuthnAdapter, _ *webauthn.SessionData, _ *webauthnAssertionResponseFields) {
				adapter.webauthn.Config.RPOrigins = []string{
					"https://wrong.example.org",
				}
			},
		},
		{
			"user handle",
			func(_ *WebAuthnAdapter, _ *webauthn.SessionData, fields *webauthnAssertionResponseFields) {
				fields.userHandle = []byte("wrong-user-handle")
			},
		},
		{
			"response credential ID",
			func(_ *WebAuthnAdapter, _ *webauthn.SessionData, fields *webauthnAssertionResponseFields) {
				fields.credentialID = []byte("changed")
			},
		},
		{
			"allow list",
			func(_ *WebAuthnAdapter, session *webauthn.SessionData, _ *webauthnAssertionResponseFields) {
				session.AllowedCredentialIDs = [][]byte{[]byte("changed")}
			},
		},
		{
			"UV requirement",
			func(_ *WebAuthnAdapter, session *webauthn.SessionData, _ *webauthnAssertionResponseFields) {
				session.UserVerification = protocol.VerificationPreferred
			},
		},
		{
			"signed UV flag",
			func(_ *WebAuthnAdapter, _ *webauthn.SessionData, fields *webauthnAssertionResponseFields) {
				fields.authenticatorData = append(
					[]byte(nil),
					vector.parsed.Raw.AssertionResponse.AuthenticatorData...,
				)
				fields.authenticatorData[32] &^= byte(protocol.FlagUserVerified)
			},
		},
		{
			"malformed response",
			func(_ *WebAuthnAdapter, _ *webauthn.SessionData, fields *webauthnAssertionResponseFields) {
				fields.credentialID = nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, user, identity, row := webauthnVectorFixtureFor(t, vector)
			session := webauthnVectorAssertionSession(
				vector,
				identity.UserHandle,
			)
			fields := webauthnAssertionResponseFields{
				credentialID: vector.credentialID,
			}
			tt.mutate(adapter, &session, &fields)
			body := webauthnAssertionBody(t, vector, fields)
			if tt.name == "malformed response" {
				body = []byte("{")
			}

			_, err := adapter.FinishAssertion(AssertionBinding{
				User: user, Identity: identity, Rows: []db.WebauthnCredential{row}, Session: session,
				Request: httptest.NewRequest(
					http.MethodPost,
					"/",
					bytes.NewReader(body),
				),
			})
			if !errors.Is(err, ErrWebAuthnAssertionRejected) {
				t.Fatalf(
					"finish assertion error = %v, want generic rejection",
					err,
				)
			}
		})
	}
}

func TestWebAuthn_FinishAssertionMapsRPAndCredentialOwner(t *testing.T) {
	// Given: the official UV assertion and one valid known-user binding.
	vector := webauthnAssertionNoneES256LongCredentialID(t)
	tests := []struct {
		name   string
		mutate func(*webauthn.SessionData, *db.WebauthnCredential)
	}{
		{
			"RP ID",
			func(session *webauthn.SessionData, _ *db.WebauthnCredential) {
				session.RelyingPartyID = "wrong.example.org"
			},
		},
		{
			"credential owner",
			func(_ *webauthn.SessionData, credential *db.WebauthnCredential) {
				credential.UserID++
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, user, identity, row := webauthnVectorFixtureFor(t, vector)
			session := webauthnVectorAssertionSession(
				vector,
				identity.UserHandle,
			)
			tt.mutate(&session, &row)

			_, err := adapter.FinishAssertion(AssertionBinding{
				User: user, Identity: identity, Rows: []db.WebauthnCredential{row}, Session: session,
				Request: httptest.NewRequest(
					http.MethodPost,
					"/",
					bytes.NewReader(vector.body),
				),
			})
			if !errors.Is(err, ErrWebAuthnAssertionRejected) {
				t.Fatalf(
					"finish assertion error = %v, want generic rejection",
					err,
				)
			}
		})
	}
}
