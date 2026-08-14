package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestAdminMFAReset_DoesNotResumeAfterFailedPassword(t *testing.T) {
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
	seedTOTP(t, h, *target.user, box)
	staleReset := beginStaleAdminMFAReset(t, h, adminMFAResetTarget{
		Admin: admin, UserID: target.user.ID,
	})
	defer staleReset.Body.Close()

	// When
	response := postPasswordReauthentication(t, h, admin, "wrong", true)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("failed reauthentication = %d, want 422", response.StatusCode)
	}
	assertNoAdminMFAReset(t, h, target.user.ID)
}

func TestAdminMFAReset_DoesNotResumeExpiredOrAlteredContinuation(t *testing.T) {
	for _, test := range []struct {
		name     string
		ceremony func(adminID int64) string
		expire   bool
	}{
		{name: "expired", expire: true},
		{
			name:     "malformed",
			ceremony: func(int64) string { return `not-json` },
		},
		{
			name: "self target",
			ceremony: func(adminID int64) string {
				return fmt.Sprintf(
					`{"target_user_id":%d,"reason":"lost_device"}`,
					adminID,
				)
			},
		},
		{
			name: "invalid target",
			ceremony: func(int64) string {
				return `{"target_user_id":999999,"reason":"lost_device"}`
			},
		},
		{
			name: "invalid reason",
			ceremony: func(int64) string {
				return `{"target_user_id":2,"reason":"untrusted"}`
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			box := configureMFA(t, h, mfa.Config{})
			admin := seedSessionAs(
				t,
				h.repo,
				h.server,
				"admin@example.com",
				"admin",
			)
			target := seedSessionAs(
				t,
				h.repo,
				h.server,
				"target@example.com",
				"deployer",
			)
			seedTOTP(t, h, *target.user, box)
			staleReset := beginStaleAdminMFAReset(
				t,
				h,
				adminMFAResetTarget{Admin: admin, UserID: target.user.ID},
			)
			defer staleReset.Body.Close()
			if test.expire {
				alterAdminMFAResetContinuation(
					t,
					h,
					admin,
					`{"target_user_id":2,"reason":"lost_device"}`,
				)
				if _, err := h.repo.DB.ExecContext(
					context.Background(),
					`UPDATE mfa_challenges SET expires_at = 0
WHERE user_id = ? AND session_id = ? AND purpose = ?`,
					admin.user.ID,
					admin.sessionToken,
					string(mfa.ChallengePurposeAdminMFAReset),
				); err != nil {
					t.Fatalf("expire reset continuation: %v", err)
				}
			} else {
				alterAdminMFAResetContinuation(
					t,
					h,
					admin,
					test.ceremony(admin.user.ID),
				)
			}

			// When
			response := postPasswordReauthentication(
				t,
				h,
				admin,
				"testpass",
				true,
			)
			defer response.Body.Close()

			// Then
			if response.StatusCode != http.StatusSeeOther ||
				response.Header.Get("Location") != "/settings/security" {
				t.Fatalf(
					"reauthentication response = %d %q, want safe redirect",
					response.StatusCode,
					response.Header.Get("Location"),
				)
			}
			assertNoAdminMFAReset(t, h, target.user.ID)
		})
	}
}

func TestAdminMFAReset_DoesNotResumeAfterAdminRoleChanges(t *testing.T) {
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
	seedTOTP(t, h, *target.user, box)
	staleReset := beginStaleAdminMFAReset(t, h, adminMFAResetTarget{
		Admin: admin, UserID: target.user.ID,
	})
	defer staleReset.Body.Close()
	if _, err := h.repo.DB.ExecContext(
		context.Background(),
		"UPDATE users SET role = 'deployer' WHERE id = ?",
		admin.user.ID,
	); err != nil {
		t.Fatalf("demote admin: %v", err)
	}

	// When
	response := postPasswordReauthentication(t, h, admin, "testpass", true)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/settings/security" {
		t.Fatalf("role-changed reauth = %d %q", response.StatusCode,
			response.Header.Get("Location"))
	}
	assertNoAdminMFAReset(t, h, target.user.ID)
}

func TestAdminMFAReset_RequiresCSRFForReauthentication(t *testing.T) {
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
	seedTOTP(t, h, *target.user, box)
	staleReset := beginStaleAdminMFAReset(t, h, adminMFAResetTarget{
		Admin: admin, UserID: target.user.ID,
	})
	defer staleReset.Body.Close()

	// When
	response := postPasswordReauthentication(t, h, admin, "testpass", false)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"missing CSRF reauthentication = %d, want 403",
			response.StatusCode,
		)
	}
	assertNoAdminMFAReset(t, h, target.user.ID)
}
