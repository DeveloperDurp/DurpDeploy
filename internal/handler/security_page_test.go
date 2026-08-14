package handler_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestSecurityPage_renders_own_factor_state_for_every_role(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	admin := seedSessionAs(
		t,
		h.repo,
		h.server,
		"security-admin@example.com",
		"admin",
	)
	viewer := seedSessionAs(
		t,
		h.repo,
		h.server,
		"security-viewer@example.com",
		"viewer",
	)

	// When
	for _, session := range []*authedSession{admin, viewer} {
		response, err := session.client.Get(h.server + "/settings/security")
		if err != nil {
			t.Fatalf("GET security page: %v", err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatalf("read security page: %v", err)
		}

		// Then
		if response.StatusCode != http.StatusOK {
			t.Fatalf("security page status = %d, want 200", response.StatusCode)
		}
		for _, text := range []string{
			"Security",
			"No factors enrolled",
			"Set up authenticator",
			"Add passkey",
			"Reauthenticate",
		} {
			if !strings.Contains(string(body), text) {
				t.Errorf("security page omitted %q", text)
			}
		}
		if strings.Contains(string(body), "/settings/tokens") &&
			session.user.Role == "viewer" {
			t.Error("viewer security page exposed Tokens")
		}
	}
}

func TestUsersList_renders_admin_mfa_reset_for_other_users(t *testing.T) {
	// Given
	fixture := newMobileStructuralFixture(t)

	// When
	body := fixture.getHTML(t, fixture.admin, "/admin/users")

	// Then
	requireHTMLPattern(
		t,
		body,
		`(?s)<form method="post" action="/admin/users/\d+/mfa-reset"[^>]*>.*?name="reason" value="administrative_reset".*?>Reset MFA</button>`,
	)
}

func TestSecurityPage_renders_enrolled_factor_metadata(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	session := seedSessionAs(
		t,
		h.repo,
		h.server,
		"security-factors@example.com",
		"deployer",
	)
	seedTOTP(t, h, *session.user, box)
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(session.user.ID, []byte("security-passkey")),
	); err != nil {
		t.Fatalf("create passkey: %v", err)
	}

	// When
	response, err := session.client.Get(h.server + "/settings/security")
	if err != nil {
		t.Fatalf("GET security page: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read security page: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("security page status = %d, want 200", response.StatusCode)
	}
	for _, text := range []string{
		"MFA is enabled with 2 factor(s).",
		"Authenticator app enrolled",
		"1 passkey(s)",
		"primary",
		"Regenerate recovery codes",
		"Disable MFA",
	} {
		if !strings.Contains(string(body), text) {
			t.Errorf("security page omitted enrolled factor metadata %q", text)
		}
	}
}

func TestSecurityPage_renders_factor_specific_enrollment_actions(t *testing.T) {
	tests := []struct {
		name            string
		totp            bool
		passkey         bool
		totpState       string
		passState       string
		totpDisabled    bool
		passkeyDisabled bool
	}{
		{
			name: "no configured factors",
		},
		{
			name:         "TOTP configured",
			totp:         true,
			totpState:    "Authenticator already configured",
			totpDisabled: true,
		},
		{
			name:            "passkey configured",
			passkey:         true,
			passState:       "Passkey already configured",
			passkeyDisabled: true,
		},
		{
			name:            "both configured",
			totp:            true,
			passkey:         true,
			totpState:       "Authenticator already configured",
			passState:       "Passkey already configured",
			totpDisabled:    true,
			passkeyDisabled: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			box := configureMFA(t, h, mfa.Config{})
			session := seedSession(t, h.repo, h.server, "deployer")
			if test.totp {
				seedTOTP(t, h, *session.user, box)
			}
			if test.passkey {
				if _, err := h.repo.Queries.CreateWebAuthnCredential(
					context.Background(),
					securityCredentialParams(
						session.user.ID,
						[]byte("configured-passkey"),
					),
				); err != nil {
					t.Fatalf("create passkey: %v", err)
				}
			}

			// When
			response, err := session.client.Get(h.server + "/settings/security")
			if err != nil {
				t.Fatalf("GET security page: %v", err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatalf("read security page: %v", err)
			}

			// Then
			if response.StatusCode != http.StatusOK {
				t.Fatalf(
					"security page status = %d, want 200",
					response.StatusCode,
				)
			}
			content := string(body)
			for _, text := range []string{"Authenticator app", "Add passkey"} {
				if !strings.Contains(content, text) {
					t.Errorf("security page omitted enrollment card %q", text)
				}
			}
			for _, state := range []string{test.totpState, test.passState} {
				if state != "" && !strings.Contains(content, state) {
					t.Errorf("security page omitted factor state %q", state)
				}
			}
			if got := strings.Contains(
				enrollmentButton(
					content,
					"/settings/security/totp/begin",
					"Set up authenticator",
				),
				"disabled",
			); got != test.totpDisabled {
				t.Errorf(
					"TOTP button disabled = %t, want %t",
					got,
					test.totpDisabled,
				)
			}
			if got := strings.Contains(
				enrollmentButton(
					content,
					"/settings/security/passkeys/begin",
					"Add passkey",
				),
				"disabled",
			); got != test.passkeyDisabled {
				t.Errorf(
					"passkey button disabled = %t, want %t",
					got,
					test.passkeyDisabled,
				)
			}
		})
	}
}

func enrollmentButton(content string, action string, label string) string {
	pattern := regexp.MustCompile(
		`(?s)<form[^>]*action="` + regexp.QuoteMeta(action) +
			`"[^>]*>.*?(<button[^>]*>` + regexp.QuoteMeta(label) + `</button>)`,
	)
	match := pattern.FindStringSubmatch(content)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func TestWebAuthnBrowserSource_uses_native_same_origin_ceremonies(
	t *testing.T,
) {
	// Given
	source, err := os.ReadFile(
		filepath.Join("..", "..", "static", "js", "app.js"),
	)
	if err != nil {
		t.Fatalf("read browser source: %v", err)
	}

	// When
	content := string(source)

	// Then
	for _, marker := range []string{
		"navigator.credentials.create",
		"navigator.credentials.get",
		"base64URLToBuffer",
		"bufferToBase64URL",
		"credentials: 'same-origin'",
		"AbortError",
	} {
		if !strings.Contains(content, marker) {
			t.Errorf("WebAuthn browser source omitted %q", marker)
		}
	}
	if strings.Contains(content, "sessionStorage") {
		t.Error("WebAuthn browser source must not use session storage")
	}
}
