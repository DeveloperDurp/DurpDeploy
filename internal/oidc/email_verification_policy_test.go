package oidc

import (
	"errors"
	"testing"
)

func TestLoadConfigEmailVerificationPolicy(t *testing.T) {
	tests := []struct {
		name                     string
		value                    string
		set                      bool
		wantErr                  bool
		wantAllowUnverifiedEmail bool
	}{
		{
			name:                     "defaults to requiring verified email",
			wantAllowUnverifiedEmail: false,
		},
		{
			name:                     "accepts lowercase true",
			value:                    "true",
			set:                      true,
			wantAllowUnverifiedEmail: false,
		},
		{
			name:                     "accepts lowercase false opt out",
			value:                    "false",
			set:                      true,
			wantAllowUnverifiedEmail: true,
		},
		{
			name:    "rejects empty value",
			value:   "",
			set:     true,
			wantErr: true,
		},
		{
			name:    "rejects invalid value",
			value:   "not-a-bool",
			set:     true,
			wantErr: true,
		},
		{
			name:    "rejects numeric value",
			value:   "1",
			set:     true,
			wantErr: true,
		},
		{
			name:    "rejects uppercase true",
			value:   "TRUE",
			set:     true,
			wantErr: true,
		},
		{
			name:    "rejects uppercase false",
			value:   "FALSE",
			set:     true,
			wantErr: true,
		},
		{
			name:    "rejects padded false",
			value:   " false ",
			set:     true,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a complete OIDC configuration and one policy value.
			clearOIDCEnv(t)
			for key, value := range completeEnv() {
				t.Setenv(key, value)
			}
			if tt.set {
				t.Setenv("DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED", tt.value)
			}

			// When: startup parses the email-verification policy.
			config, err := LoadConfig()

			// Then: only exact lowercase values configure the policy.
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf(
						"LoadConfig() error = %v, want ErrInvalidConfig",
						err,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if config.AllowUnverifiedEmail != tt.wantAllowUnverifiedEmail {
				t.Fatalf(
					"AllowUnverifiedEmail = %t, want %t",
					config.AllowUnverifiedEmail,
					tt.wantAllowUnverifiedEmail,
				)
			}
		})
	}
}

func TestParseClaimsEmailVerificationPolicy(t *testing.T) {
	tests := []struct {
		name                 string
		claims               string
		allowUnverifiedEmail bool
		want                 ClaimIdentity
		wantField            string
		wantReason           ClaimErrorReason
	}{
		{
			name:                 "rejects false boolean claim when verification is required",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":false,"groups":["durp-viewer"]}`,
			allowUnverifiedEmail: false,
			wantField:            "email_verified",
			wantReason:           ClaimUnverified,
		},
		{
			name:                 "accepts false boolean claim only when opted out",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":false,"groups":["durp-viewer"]}`,
			allowUnverifiedEmail: true,
			want: ClaimIdentity{
				Issuer:  testIssuer,
				Subject: "person-123",
				Email:   "person@example.com",
				Name:    "person@example.com",
				Role:    "viewer",
			},
		},
		{
			name:                 "accepts true boolean claim when opted out",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":true,"groups":["durp-viewer"]}`,
			allowUnverifiedEmail: true,
			want: ClaimIdentity{
				Issuer:  testIssuer,
				Subject: "person-123",
				Email:   "person@example.com",
				Name:    "person@example.com",
				Role:    "viewer",
			},
		},
		{
			name:                 "rejects missing claim when verification is required",
			claims:               `{"sub":"person-123","email":"person@example.com","groups":["durp-viewer"]}`,
			allowUnverifiedEmail: false,
			wantField:            "email_verified",
			wantReason:           ClaimMissing,
		},
		{
			name:                 "rejects null claim when opted out",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":null,"groups":["durp-viewer"]}`,
			allowUnverifiedEmail: true,
			wantField:            "email_verified",
			wantReason:           ClaimWrongType,
		},
		{
			name:                 "rejects missing claim when opted out",
			claims:               `{"sub":"person-123","email":"person@example.com","groups":["durp-viewer"]}`,
			allowUnverifiedEmail: true,
			wantField:            "email_verified",
			wantReason:           ClaimMissing,
		},
		{
			name:                 "rejects string claim when verification is required",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":"true","groups":["durp-viewer"]}`,
			allowUnverifiedEmail: false,
			wantField:            "email_verified",
			wantReason:           ClaimWrongType,
		},
		{
			name:                 "rejects numeric claim when opted out",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":1,"groups":["durp-viewer"]}`,
			allowUnverifiedEmail: true,
			wantField:            "email_verified",
			wantReason:           ClaimWrongType,
		},
		{
			name:                 "rejects string claim when opted out",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":"true","groups":["durp-viewer"]}`,
			allowUnverifiedEmail: true,
			wantField:            "email_verified",
			wantReason:           ClaimWrongType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: verified token claims with an explicit policy decision.
			// When: the provider-neutral claim mapper validates email verification.
			identity, err := ParseClaims(
				[]byte(tt.claims),
				testIssuer,
				testGroupMapping,
				tt.allowUnverifiedEmail,
			)

			// Then: opt-out still accepts only literal boolean claims.
			if tt.wantReason != "" {
				var claimErr *ClaimError
				if !errors.As(err, &claimErr) {
					t.Fatalf("ParseClaims() error = %v, want ClaimError", err)
				}
				if claimErr.Field != tt.wantField ||
					claimErr.Reason != tt.wantReason {
					t.Fatalf(
						"ClaimError = {%q, %q}, want {%q, %q}",
						claimErr.Field,
						claimErr.Reason,
						tt.wantField,
						tt.wantReason,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseClaims() error = %v", err)
			}
			if identity != tt.want {
				t.Fatalf("ParseClaims() = %#v, want %#v", identity, tt.want)
			}
		})
	}
}
