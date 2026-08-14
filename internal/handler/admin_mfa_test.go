package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestAdminMFAResetRejectsUnauthorizedTargets(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"target@example.com",
		"deployer",
	)
	deployer := seedSessionAs(
		t,
		h.repo,
		h.server,
		"deployer@example.com",
		"deployer",
	)
	admin := seedSessionAs(t, h.repo, h.server, "admin@example.com", "admin")
	markSessionFresh(t, h, admin.sessionToken)

	// When / Then
	cases := []struct {
		name       string
		actor      *authedSession
		target     int64
		fresh      bool
		wantStatus int
		wantPath   string
	}{
		{
			"deployer",
			deployer,
			target.user.ID,
			true,
			http.StatusForbidden,
			"",
		},
		{
			"stale admin",
			admin,
			target.user.ID,
			false,
			http.StatusSeeOther,
			"/settings/security/reauth",
		},
		{
			"self",
			admin,
			admin.user.ID,
			true,
			http.StatusForbidden,
			"",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if test.fresh {
				markSessionFresh(t, h, test.actor.sessionToken)
			} else if err := h.repo.Queries.MarkSessionReauthenticated(
				context.Background(),
				db.MarkSessionReauthenticatedParams{
					ID: test.actor.sessionToken,
				},
			); err != nil {
				t.Fatalf("make admin stale: %v", err)
			}

			response, err := test.actor.client.PostForm(
				h.server+"/admin/users/"+strconv.FormatInt(
					test.target,
					10,
				)+"/mfa-reset",
				url.Values{
					"csrf_token": {test.actor.csrfToken},
					"reason":     {"lost_device"},
				},
			)
			if err != nil {
				t.Fatalf("POST admin MFA reset: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.wantStatus ||
				response.Header.Get("Location") != test.wantPath {
				t.Errorf(
					"response = %d %q, want %d %q",
					response.StatusCode,
					response.Header.Get("Location"),
					test.wantStatus,
					test.wantPath,
				)
			}
		})
	}
}

func TestAdminMFAResetRemovesBrowserMFAStatePreservesAPITokenAndAudits(
	t *testing.T,
) {
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
	markSessionFresh(t, h, admin.sessionToken)
	seedTOTP(t, h, *target.user, box)
	seedRecoveryCode(t, h, target.user.ID)
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
	response, err := admin.client.PostForm(
		h.server+"/admin/users/"+strconv.FormatInt(
			target.user.ID,
			10,
		)+"/mfa-reset",
		url.Values{
			"csrf_token": {admin.csrfToken},
			"reason":     {"lost_device"},
		},
	)
	if err != nil {
		t.Fatalf("POST admin MFA reset: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset status = %d, want 303", response.StatusCode)
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
	} {
		if countSecurityRows(t, h, query, target.user.ID) != 0 {
			t.Fatalf("reset retained target state for query %q", query)
		}
	}

	request, err := http.NewRequest(
		http.MethodGet,
		h.server+"/api/v1/users/me",
		nil,
	)
	if err != nil {
		t.Fatalf("new token request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiToken)
	tokenResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("use preserved API token: %v", err)
	}
	defer tokenResponse.Body.Close()
	if tokenResponse.StatusCode != http.StatusOK {
		t.Errorf(
			"preserved API token status = %d, want 200",
			tokenResponse.StatusCode,
		)
	}

	entries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 10,
			FAction:   sql.NullString{String: "mfa_admin_reset", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("list reset audit rows: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("reset audit rows = %d, want 1", len(entries))
	}
	if !entries[0].EntityID.Valid ||
		entries[0].EntityID.Int64 != target.user.ID {
		t.Errorf(
			"reset audit entity = %#v, want target user %d",
			entries[0].EntityID,
			target.user.ID,
		)
	}
	if !strings.Contains(entries[0].Details.String, "lost_device") {
		t.Fatal("reset audit details omitted the canonical reason")
	}
	if strings.Contains(entries[0].Details.String, apiToken) ||
		strings.Contains(entries[0].Details.String, "challenge") {
		t.Fatal("reset audit details contain secret protocol data")
	}
}

func seedRecoveryCode(t *testing.T, h *authHarness, userID int64) {
	t.Helper()
	hash, err := mfa.HashRecoveryCode("0123-4567-89AB-CDEF-0123-4567-89AB-CDEF")
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	if _, err := h.repo.Queries.CreateRecoveryCode(
		context.Background(),
		db.CreateRecoveryCodeParams{
			ID: "admin-reset-recovery", UserID: userID, CodeHash: hash[:],
		},
	); err != nil {
		t.Fatalf("create recovery code: %v", err)
	}
}

func seedAPIToken(t *testing.T, h *authHarness, userID int64) string {
	t.Helper()
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint API token: %v", err)
	}
	if _, err := h.repo.Queries.CreateApiToken(
		context.Background(),
		db.CreateApiTokenParams{
			ID: "admin-reset-api-token", UserID: userID, Name: "preserved",
			TokenPrefix: prefix, TokenHash: hash, Scope: "global",
		},
	); err != nil {
		t.Fatalf("create API token: %v", err)
	}
	return token
}
