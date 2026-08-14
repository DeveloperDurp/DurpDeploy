package handler_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestSecurity_RecoveryRegenerationReturnsDirectlyToSecurity(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	seedTOTP(t, h, *current.user, box)

	// When
	regenerated := postSecurityValues(t, current, h.server,
		"/settings/security/recovery/regenerate", url.Values{
			"csrf_token": {current.csrfToken},
		})
	defer regenerated.Body.Close()
	body, err := io.ReadAll(regenerated.Body)
	if err != nil {
		t.Fatalf("read regenerated recovery page: %v", err)
	}
	security, err := current.client.Get(h.server + "/settings/security")
	if err != nil {
		t.Fatalf("get security after recovery regeneration: %v", err)
	}
	defer security.Body.Close()

	// Then
	if regenerated.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "Back to Security") ||
		strings.Contains(
			string(body),
			"Continue",
		) || security.StatusCode != http.StatusOK {
		t.Fatalf(
			"regeneration/security responses = %d / %d",
			regenerated.StatusCode,
			security.StatusCode,
		)
	}
}
