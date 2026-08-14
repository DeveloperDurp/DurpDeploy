package handler_test

import (
	"io"
	"net/http"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestAdminMFAReset_DoesNotResumeAfterFailedFactor(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	admin := seedSessionAs(t, h.repo, h.server, "admin@example.com", "admin")
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"target@example.com",
		"deployer",
	)
	seedTOTP(t, h, *admin.user, box)
	seedTOTP(t, h, *target.user, box)
	staleReset := beginStaleAdminMFAReset(t, h, adminMFAResetTarget{
		Admin: admin, UserID: target.user.ID,
	})
	defer staleReset.Body.Close()
	password := postPasswordReauthentication(t, h, admin, "testpass", true)
	defer password.Body.Close()
	if password.StatusCode != http.StatusOK {
		t.Fatalf(
			"password reauthentication = %d, want factor prompt",
			password.StatusCode,
		)
	}
	factorBody, err := io.ReadAll(password.Body)
	if err != nil {
		t.Fatalf("read factor prompt: %v", err)
	}
	factor := securityHiddenValues(t, string(factorBody))
	factor.Set("csrf_token", admin.csrfToken)
	factor.Set("code", "000000")

	// When
	response, err := admin.client.PostForm(
		h.server+"/settings/security/reauth/totp",
		factor,
	)
	if err != nil {
		t.Fatalf("POST failed factor reauthentication: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"failed factor reauthentication = %d, want 422",
			response.StatusCode,
		)
	}
	assertNoAdminMFAReset(t, h, target.user.ID)
}
