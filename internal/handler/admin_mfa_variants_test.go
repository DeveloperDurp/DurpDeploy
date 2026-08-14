package handler_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestAdminMFAResetCleansPasskeyTargetAndPreservesAPIToken(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	admin := seedSessionAs(t, h.repo, h.server, "admin@example.com", "admin")
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"target@example.com",
		"deployer",
	)
	markSessionFresh(t, h, admin.sessionToken)
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(target.user.ID, []byte{7}),
	); err != nil {
		t.Fatalf("create passkey: %v", err)
	}
	if _, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: h.repo,
	}).Issue(context.Background(), mfa.ChallengeIssue{
		UserID: target.user.ID, SessionID: target.sessionToken,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	}); err != nil {
		t.Fatalf("create target MFA challenge: %v", err)
	}
	apiToken := seedAPIToken(t, h, target.user.ID)

	// When
	response := postAdminMFAReset(t, h, adminMFAResetTarget{
		Admin:  admin,
		UserID: target.user.ID,
	})
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset status = %d, want 303", response.StatusCode)
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?",
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
	} {
		if countSecurityRows(t, h, query, target.user.ID) != 0 {
			t.Fatalf("reset retained target state for query %q", query)
		}
	}
	assertAPITokenWorks(t, h, apiToken)
}

func TestAdminMFAResetCleansNoFactorTargetAndPreservesAPIToken(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	admin := seedSessionAs(t, h.repo, h.server, "admin@example.com", "admin")
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"target@example.com",
		"deployer",
	)
	markSessionFresh(t, h, admin.sessionToken)
	if _, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: h.repo,
	}).Issue(context.Background(), mfa.ChallengeIssue{
		UserID: target.user.ID, SessionID: target.sessionToken,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	}); err != nil {
		t.Fatalf("create target MFA challenge: %v", err)
	}
	apiToken := seedAPIToken(t, h, target.user.ID)

	// When
	response := postAdminMFAReset(t, h, adminMFAResetTarget{
		Admin:  admin,
		UserID: target.user.ID,
	})
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset status = %d, want 303", response.StatusCode)
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		"SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?",
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
	} {
		if countSecurityRows(t, h, query, target.user.ID) != 0 {
			t.Fatalf("reset retained target state for query %q", query)
		}
	}
	assertAPITokenWorks(t, h, apiToken)
}

type adminMFAResetTarget struct {
	Admin  *authedSession
	UserID int64
}

func postAdminMFAReset(
	t *testing.T,
	h *authHarness,
	target adminMFAResetTarget,
) *http.Response {
	t.Helper()
	response, err := target.Admin.client.PostForm(
		h.server+"/admin/users/"+strconv.FormatInt(
			target.UserID,
			10,
		)+"/mfa-reset",
		url.Values{
			"csrf_token": {target.Admin.csrfToken},
			"reason":     {"lost_device"},
		},
	)
	if err != nil {
		t.Fatalf("POST admin MFA reset: %v", err)
	}
	return response
}

func assertAPITokenWorks(t *testing.T, h *authHarness, token string) {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodGet,
		h.server+"/api/v1/users/me",
		nil,
	)
	if err != nil {
		t.Fatalf("new token request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("use preserved API token: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf(
			"preserved API token status = %d, want 200",
			response.StatusCode,
		)
	}
}
