package handler_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"durpdeploy/internal/mfa"
)

func TestSecurity_TOTPEnrollmentRejectsExpiredAndWrongUserChallenges(
	t *testing.T,
) {
	tests := []struct {
		name string
		run  func(*testing.T, *authHarness, *authedSession, stagedTOTP)
	}{
		{
			name: "expired",
			run: func(t *testing.T, h *authHarness, current *authedSession, staged stagedTOTP) {
				if _, err := h.repo.DB.ExecContext(
					context.Background(),
					"UPDATE mfa_challenges SET expires_at = ?",
					time.Now().Add(-time.Second).Unix(),
				); err != nil {
					t.Fatalf("expire TOTP test challenge: %v", err)
				}
				response := postSecurityValues(
					t,
					current,
					h.server,
					"/settings/security/totp/confirm",
					enrollmentValues(
						staged,
						totpCode(
							t,
							staged.seed,
							time.Now(),
						),
					),
				)
				defer response.Body.Close()
				if response.StatusCode != http.StatusUnprocessableEntity {
					t.Fatalf(
						"expired enrollment = %d, want 422",
						response.StatusCode,
					)
				}
			},
		},
		{
			name: "wrong user",
			run: func(t *testing.T, h *authHarness, current *authedSession, staged stagedTOTP) {
				other := seedSessionAs(
					t,
					h.repo,
					h.server,
					"other@example.test",
					"deployer",
				)
				values := enrollmentValues(
					staged,
					totpCode(t, staged.seed, time.Now()),
				)
				values.Set("csrf_token", other.csrfToken)
				response := postSecurityValues(
					t,
					other,
					h.server,
					"/settings/security/totp/confirm",
					values,
				)
				defer response.Body.Close()
				if response.StatusCode != http.StatusUnprocessableEntity {
					t.Fatalf(
						"wrong-user enrollment = %d, want 422",
						response.StatusCode,
					)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			configureMFA(t, h, mfa.Config{})
			current := seedSession(t, h.repo, h.server, "admin")
			staged := stageTOTP(t, h, current)

			// When
			test.run(t, h, current, staged)

			// Then
			if countSecurityRows(
				t,
				h,
				"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
				current.user.ID,
			) != 0 ||
				countSecurityRows(
					t,
					h,
					"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
					current.user.ID,
				) != 0 {
				t.Fatal(
					"invalid enrollment challenge activated TOTP or disclosed recovery codes",
				)
			}
		})
	}
}
