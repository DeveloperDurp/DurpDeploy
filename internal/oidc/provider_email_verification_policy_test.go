package oidc

import (
	"errors"
	"strconv"
	"testing"
)

func TestProviderEmailVerificationPolicy(t *testing.T) {
	tests := []struct {
		name            string
		allowUnverified bool
		reauthenticate  bool
		wantReason      ClaimErrorReason
	}{
		{name: "rejects false by default", wantReason: ClaimUnverified},
		{name: "allows false opt-out login", allowUnverified: true},
		{
			name:            "allows false opt-out reauthentication",
			allowUnverified: true,
			reauthenticate:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			fixture := newProviderFixture(t)
			if tt.allowUnverified {
				fixture.config.AllowUnverifiedEmail = true
			}
			fixture.config.GroupClaim = "groups"
			fixture.config.AdminGroup = "admin"
			fixture.config.DeployerGroup = "deployer"
			fixture.config.ViewerGroup = "viewer"
			provider := fixture.newProvider(t)
			claims := []byte(
				`{"sub":"person-123","email":"person@example.com",` +
					`"email_verified":false,"groups":["viewer"],"auth_time":` +
					strconv.FormatInt(fixture.now.Unix(), 10) + `}`,
			)

			// When
			var err error
			if tt.reauthenticate {
				transaction := fixture.transaction
				transaction.Mode = TransactionModeReauth
				transaction.ExpiresAt = fixture.now.Add(transactionLifetime)
				_, err = provider.ParseReauthenticationClaims(
					claims,
					transaction,
				)
			} else {
				_, err = provider.ParseClaims(claims)
			}

			// Then
			if tt.wantReason == "" {
				if err != nil {
					t.Fatalf("claim parsing error = %v", err)
				}
				return
			}
			var claimErr *ClaimError
			if !errors.As(err, &claimErr) {
				t.Fatalf("claim parsing error = %v, want ClaimError", err)
			}
			if claimErr.Field != "email_verified" ||
				claimErr.Reason != tt.wantReason {
				t.Fatalf(
					"ClaimError = {%q, %q}, want {email_verified, %q}",
					claimErr.Field,
					claimErr.Reason,
					tt.wantReason,
				)
			}
		})
	}
}
