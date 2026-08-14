package handler_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestLogin_MFAChallengePersistsWhenTOTPIsRejected(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "mfa-availability-baseline@example.com", "hunter2")
	seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)

	// When
	response, err := pending.client.PostForm(
		h.server+"/login/mfa/totp",
		url.Values{"code": {"000000"}, "csrf_token": {pending.csrf}},
	)
	if err != nil {
		t.Fatalf("post rejected TOTP: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status = %d, want %d",
			response.StatusCode,
			http.StatusUnprocessableEntity,
		)
	}
	var challenges int
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		user.ID,
	).Scan(&challenges); err != nil {
		t.Fatalf("count pending MFA challenges: %v", err)
	}
	if challenges != 1 {
		t.Fatalf("pending MFA challenges = %d, want 1", challenges)
	}
}

func TestLogin_MFAAvailabilityRendersOnlyConfiguredMethods(t *testing.T) {
	tests := []struct {
		name     string
		totp     bool
		passkey  bool
		recovery bool
		exhaust  bool
		want     []string
		absent   []string
	}{
		{
			name: "TOTP only", totp: true,
			want:   []string{`action="/login/mfa/totp"`},
			absent: unavailableLoginMFAMethods(`action="/login/mfa/totp"`),
		},
		{
			name: "passkey only", passkey: true,
			want:   []string{`id="login-mfa-passkey"`},
			absent: unavailableLoginMFAMethods(`id="login-mfa-passkey"`),
		},
		{
			name: "TOTP and passkey", totp: true, passkey: true,
			want: []string{
				`action="/login/mfa/totp"`,
				`id="login-mfa-passkey"`,
			},
			absent: unavailableLoginMFAMethods(
				`action="/login/mfa/totp"`,
				`id="login-mfa-passkey"`,
			),
		},
		{
			name: "unused recovery code", recovery: true,
			want:   []string{`action="/login/mfa/recovery"`},
			absent: unavailableLoginMFAMethods(`action="/login/mfa/recovery"`),
		},
		{
			name: "exhausted recovery code", totp: true, recovery: true, exhaust: true,
			want:   []string{`action="/login/mfa/totp"`},
			absent: unavailableLoginMFAMethods(`action="/login/mfa/totp"`),
		},
		{
			name:   "no factors",
			absent: unavailableLoginMFAMethods(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			box := configureMFA(t, h, mfa.Config{})
			user := h.seedUser(
				t,
				"mfa-availability-"+strings.ReplaceAll(
					test.name,
					" ",
					"-",
				)+"@example.com",
				"hunter2",
			)
			if test.totp {
				seedTOTP(t, h, user, box)
			}
			if test.passkey {
				seedLoginMFAPasskey(t, h, user.ID)
			}
			if test.recovery {
				seedLoginMFARecoveryCode(t, h, user.ID, test.exhaust)
			}
			pending := issueLoginMFAChallenge(t, h, user.ID)

			// When
			response, err := pending.client.Get(h.server + "/login/mfa")
			if err != nil {
				t.Fatalf("get MFA page: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read MFA page: %v", err)
			}
			markup := string(body)

			// Then
			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"status = %d, want %d",
					response.StatusCode,
					http.StatusOK,
				)
			}
			for _, method := range test.want {
				if !strings.Contains(markup, method) {
					t.Errorf("MFA page omitted available %q", method)
				}
			}
			for _, method := range test.absent {
				if strings.Contains(markup, method) {
					t.Errorf("MFA page rendered unavailable %q", method)
				}
			}
		})
	}
}

func unavailableLoginMFAMethods(available ...string) []string {
	methods := []string{
		`action="/login/mfa/totp"`,
		`action="/login/mfa/recovery"`,
		`id="login-mfa-passkey"`,
	}
	var unavailable []string
	for _, method := range methods {
		found := false
		for _, configured := range available {
			if method == configured {
				found = true
				break
			}
		}
		if !found {
			unavailable = append(unavailable, method)
		}
	}
	return unavailable
}

func seedLoginMFAPasskey(t *testing.T, h *authHarness, userID int64) {
	t.Helper()
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(userID, []byte("login-mfa-page-passkey")),
	); err != nil {
		t.Fatalf("create login MFA passkey: %v", err)
	}
}

func seedLoginMFARecoveryCode(
	t *testing.T,
	h *authHarness,
	userID int64,
	exhaust bool,
) {
	t.Helper()
	hash, err := mfa.HashRecoveryCode("0123456789ABCDEF0123456789ABCDEF")
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	if _, err := h.repo.Queries.CreateRecoveryCode(
		context.Background(),
		db.CreateRecoveryCodeParams{
			ID: "login-mfa-availability", UserID: userID, CodeHash: hash[:],
		},
	); err != nil {
		t.Fatalf("create recovery code: %v", err)
	}
	if !exhaust {
		return
	}
	if _, err := h.repo.DB.ExecContext(
		context.Background(),
		"UPDATE mfa_recovery_codes SET used_at = ? WHERE user_id = ?",
		sql.NullInt64{Int64: 1, Valid: true},
		userID,
	); err != nil {
		t.Fatalf("exhaust recovery code: %v", err)
	}
}
