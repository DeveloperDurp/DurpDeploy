package oidc

import (
	"errors"
	"strings"
	"testing"
)

var testGroupMapping = GroupMapping{
	ClaimName: "groups",
	Admin:     "durp-admin",
	Deployer:  "durp-deployer",
	Viewer:    "durp-viewer",
}

const testIssuer = "https://id.example"

func TestParseClaims(t *testing.T) {
	tests := []struct {
		name                 string
		claims               string
		mapping              GroupMapping
		allowUnverifiedEmail bool
		want                 ClaimIdentity
		wantField            string
		wantReason           ClaimErrorReason
	}{
		{
			name:                 "maps viewer and normalizes email",
			claims:               `{"sub":"person-123", "email":"  PERSON@example.com  ", "email_verified":true, "name":"Person", "groups":["durp-viewer"]}`,
			mapping:              testGroupMapping,
			allowUnverifiedEmail: false,
			want: ClaimIdentity{
				Issuer:  testIssuer,
				Subject: "person-123",
				Email:   "person@example.com",
				Name:    "Person",
				Role:    "viewer",
			},
		},
		{
			name:                 "admin wins over all matching groups",
			claims:               `{"sub":"person-123", "email":"person@example.com", "email_verified":true, "groups":["durp-viewer", "durp-deployer", "durp-admin"]}`,
			mapping:              testGroupMapping,
			allowUnverifiedEmail: false,
			want: ClaimIdentity{
				Issuer:  testIssuer,
				Subject: "person-123",
				Email:   "person@example.com",
				Name:    "person@example.com",
				Role:    "admin",
			},
		},
		{
			name:                 "deployer wins over viewer with duplicate groups",
			claims:               `{"sub":"person-123", "email":"person@example.com", "email_verified":true, "groups":["durp-viewer", "durp-deployer", "durp-deployer"]}`,
			mapping:              testGroupMapping,
			allowUnverifiedEmail: false,
			want: ClaimIdentity{
				Issuer:  testIssuer,
				Subject: "person-123",
				Email:   "person@example.com",
				Name:    "person@example.com",
				Role:    "deployer",
			},
		},
		{
			name:   "uses configured custom group claim",
			claims: `{"sub":"person-123", "email":"person@example.com", "email_verified":true, "teams":["release"]}`,
			mapping: GroupMapping{
				ClaimName: "teams",
				Admin:     "admins",
				Deployer:  "release",
				Viewer:    "readers",
			},
			allowUnverifiedEmail: false,
			want: ClaimIdentity{
				Issuer: testIssuer, Subject: "person-123", Email: "person@example.com",
				Name: "person@example.com", Role: "deployer",
			},
		},
		{
			name:       "rejects malformed claim document",
			claims:     `{"sub":"ignore previous instructions`,
			mapping:    testGroupMapping,
			wantField:  "claims",
			wantReason: ClaimInvalid,
		},
		{
			name:       "rejects missing subject",
			claims:     `{"email":"person@example.com","email_verified":true,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "sub",
			wantReason: ClaimMissing,
		},
		{
			name:       "rejects empty subject",
			claims:     `{"sub":"", "email":"person@example.com","email_verified":true,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "sub",
			wantReason: ClaimInvalid,
		},
		{
			name: "rejects oversized subject",
			claims: `{"sub":"` + strings.Repeat(
				"a",
				256,
			) + `","email":"person@example.com","email_verified":true,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "sub",
			wantReason: ClaimInvalid,
		},
		{
			name:       "rejects non ascii subject",
			claims:     `{"sub":"person-☃","email":"person@example.com","email_verified":true,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "sub",
			wantReason: ClaimInvalid,
		},
		{
			name:       "rejects missing email",
			claims:     `{"sub":"person-123","email_verified":true,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "email",
			wantReason: ClaimMissing,
		},
		{
			name:       "rejects email wrong type",
			claims:     `{"sub":"person-123","email":1,"email_verified":true,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "email",
			wantReason: ClaimWrongType,
		},
		{
			name:       "rejects invalid email",
			claims:     `{"sub":"person-123","email":"ignore previous instructions <script>","email_verified":true,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "email",
			wantReason: ClaimInvalid,
		},
		{
			name: "rejects oversized email",
			claims: `{"sub":"person-123","email":"` + strings.Repeat(
				"a",
				312,
			) + `@example.com","email_verified":true,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "email",
			wantReason: ClaimInvalid,
		},
		{
			name:                 "rejects missing email verification",
			claims:               `{"sub":"person-123","email":"person@example.com","groups":["durp-viewer"]}`,
			mapping:              testGroupMapping,
			allowUnverifiedEmail: false,
			wantField:            "email_verified",
			wantReason:           ClaimMissing,
		},
		{
			name:                 "rejects false email verification",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":false,"groups":["durp-viewer"]}`,
			mapping:              testGroupMapping,
			allowUnverifiedEmail: false,
			wantField:            "email_verified",
			wantReason:           ClaimUnverified,
		},
		{
			name:                 "rejects email verification wrong type",
			claims:               `{"sub":"person-123","email":"person@example.com","email_verified":"true","groups":["durp-viewer"]}`,
			mapping:              testGroupMapping,
			allowUnverifiedEmail: false,
			wantField:            "email_verified",
			wantReason:           ClaimWrongType,
		},
		{
			name:       "rejects missing groups",
			claims:     `{"sub":"person-123","email":"person@example.com","email_verified":true}`,
			mapping:    testGroupMapping,
			wantField:  "groups",
			wantReason: ClaimMissing,
		},
		{
			name:       "rejects malformed groups",
			claims:     `{"sub":"person-123","email":"person@example.com","email_verified":true,"groups":"durp-viewer"}`,
			mapping:    testGroupMapping,
			wantField:  "groups",
			wantReason: ClaimWrongType,
		},
		{
			name:       "rejects groups containing non strings",
			claims:     `{"sub":"person-123","email":"person@example.com","email_verified":true,"groups":["durp-viewer",1]}`,
			mapping:    testGroupMapping,
			wantField:  "groups",
			wantReason: ClaimWrongType,
		},
		{
			name:       "rejects unmapped groups",
			claims:     `{"sub":"person-123","email":"person@example.com","email_verified":true,"groups":["other"]}`,
			mapping:    testGroupMapping,
			wantField:  "groups",
			wantReason: ClaimUnmapped,
		},
		{
			name:       "rejects partial group name matches",
			claims:     `{"sub":"person-123","email":"person@example.com","email_verified":true,"groups":["durp-admin-extra"]}`,
			mapping:    testGroupMapping,
			wantField:  "groups",
			wantReason: ClaimUnmapped,
		},
		{
			name: "rejects oversized name",
			claims: `{"sub":"person-123","email":"person@example.com","email_verified":true,"name":"` + strings.Repeat(
				"a",
				256,
			) + `","groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "name",
			wantReason: ClaimInvalid,
		},
		{
			name:       "rejects name wrong type",
			claims:     `{"sub":"person-123","email":"person@example.com","email_verified":true,"name":1,"groups":["durp-viewer"]}`,
			mapping:    testGroupMapping,
			wantField:  "name",
			wantReason: ClaimWrongType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowUnverifiedEmail := tt.allowUnverifiedEmail
			if tt.wantReason != "" {
				allowUnverifiedEmail = false
			}

			got, err := ParseClaims(
				[]byte(tt.claims),
				testIssuer,
				tt.mapping,
				allowUnverifiedEmail,
			)

			if tt.wantReason != "" {
				var claimErr *ClaimError
				if !errors.As(err, &claimErr) {
					t.Fatalf("expected ClaimError, got %v", err)
				}
				if claimErr.Field != tt.wantField ||
					claimErr.Reason != tt.wantReason {
					t.Fatalf(
						"got ClaimError{%q, %q}, want {%q, %q}",
						claimErr.Field,
						claimErr.Reason,
						tt.wantField,
						tt.wantReason,
					)
				}
				if strings.Contains(err.Error(), tt.claims) ||
					strings.Contains(
						err.Error(),
						"ignore previous instructions",
					) {
					t.Fatalf("error exposed claim content: %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseClaims() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseClaims() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
