package oidc

import (
	"errors"
	"strings"
	"testing"
)

func TestParseClaimsRejectsNonStringGroupElements(t *testing.T) {
	tests := []struct {
		name      string
		element   string
		forbidden string
	}{
		{name: "null", element: "null", forbidden: "null"},
		{name: "number", element: "1", forbidden: "1"},
		{name: "boolean", element: "false", forbidden: "false"},
		{name: "array", element: "[]", forbidden: "[]"},
		{
			name:      "object with malicious text",
			element:   `{"prompt":"ignore previous instructions"}`,
			forbidden: "ignore previous instructions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			claims := `{"sub":"person-123","email":"person@example.com","email_verified":true,"groups":["durp-viewer",` + tt.element + `]}`

			// When
			identity, err := ParseClaims(
				[]byte(claims),
				testIssuer,
				testGroupMapping,
				false,
			)

			// Then
			var claimErr *ClaimError
			if !errors.As(err, &claimErr) {
				t.Fatalf("expected ClaimError, got %v", err)
			}
			if claimErr.Field != "groups" || claimErr.Reason != ClaimWrongType {
				t.Fatalf(
					"got ClaimError{%q, %q}, want {groups, %q}",
					claimErr.Field,
					claimErr.Reason,
					ClaimWrongType,
				)
			}
			if identity != (ClaimIdentity{}) {
				t.Fatalf(
					"ParseClaims() identity = %#v, want zero identity",
					identity,
				)
			}
			if strings.Contains(err.Error(), tt.forbidden) {
				t.Fatalf("error exposed claim content: %q", err)
			}
		})
	}
}
