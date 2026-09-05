package audit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/requestmeta"
)

func TestMFAAuditActionMapUsesStableActions(t *testing.T) {
	// Given
	cases := map[string]string{
		"POST /login/mfa/totp":                           "mfa_login_factor",
		"POST /login/mfa/recovery":                       "mfa_recovery_use",
		"POST /login/mfa/webauthn/begin":                 "",
		"POST /login/mfa/webauthn/finish":                "mfa_login_factor",
		"POST /login/mfa/cancel":                         "",
		"POST /settings/security/reauth":                 "reauthenticate",
		"POST /settings/security/reauth/totp":            "reauthenticate",
		"POST /settings/security/reauth/recovery":        "mfa_recovery_use",
		"POST /settings/security/reauth/webauthn/begin":  "",
		"POST /settings/security/reauth/webauthn/finish": "reauthenticate",
		"POST /settings/security/totp/begin":             "",
		"POST /settings/security/totp/confirm":           "mfa_totp_enroll",
		"POST /settings/security/passkeys/begin":         "",
		"POST /settings/security/passkeys/finish":        "mfa_passkey_add",
		"POST /settings/security/passkeys/rename":        "mfa_passkey_rename",
		"POST /settings/security/passkeys/delete":        "mfa_passkey_delete",
		"POST /settings/security/recovery/regenerate":    "mfa_recovery_regenerate",
		"POST /settings/security/disable":                "mfa_disable",
		"POST /admin/users/{id}/mfa-reset":               "mfa_admin_reset",
	}

	// When / Then
	for route, want := range cases {
		if got, ok := actionMap[route]; !ok || got != want {
			t.Errorf("actionMap[%q] = %q, %t; want %q", route, got, ok, want)
		}
	}
}

func TestAuditActionMapCoversReviewedRouteRegistrations(t *testing.T) {
	// Given
	cases := map[string]string{
		"POST /api/lint":                                       "",
		"POST /lifecycles/{id}":                                "update_lifecycle",
		"PATCH /projects/{id}/steps/reorder":                   "reorder_step",
		"POST /projects/{id}/steps/from-template/{templateId}": "create_step_from_template",
		"POST /projects/{id}/steps/{stepId}/save-as-template":  "create_template_from_step",
		"POST /settings/security/reauth":                       "reauthenticate",
		"POST /settings/security/reauth/totp":                  "reauthenticate",
		"POST /settings/security/reauth/webauthn/finish":       "reauthenticate",
		"POST /api/v1/admin/maintenance":                       "",
		"POST /api/v1/deployments/{id}/retry":                  "retry_deployment",
		"PUT /api/v1/projects/{id}/members/{userId}":           "update_project_member",
		"POST /api/v1/projects/{id}/deployments":               "create_deployment",
	}

	// When / Then
	for route, want := range cases {
		if got, ok := actionMap[route]; !ok || got != want {
			t.Errorf("actionMap[%q] = %q, %t; want %q", route, got, ok, want)
		}
	}
}

func TestAuditActionMapExcludesUnregisteredRoutes(t *testing.T) {
	for _, route := range []string{
		"POST /tokens",
		"DELETE /tokens/{id}",
		"DELETE /admin/tokens/{id}",
	} {
		if _, ok := actionMap[route]; ok {
			t.Errorf("actionMap contains unregistered route %q", route)
		}
	}
}

func TestMFAAuditDetailsRedactsProtocolFields(t *testing.T) {
	// Given
	values := url.Values{
		"challenge_token": {"challenge-secret"},
		"code":            {"recovery-secret"},
		"reason":          {"lost_device"},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/users/42/mfa-reset",
		strings.NewReader(values.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// When
	got := buildDetails(req, http.StatusSeeOther, "mfa_admin_reset", "")

	// Then
	var details struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(got), &details); err != nil {
		t.Fatalf("unmarshal audit details: %v", err)
	}
	if details.Reason != "lost_device" {
		t.Errorf("reason = %q, want lost_device", details.Reason)
	}
	if strings.Contains(got, "challenge-secret") ||
		strings.Contains(got, "recovery-secret") {
		t.Fatal("audit details contain MFA protocol data")
	}
}

func TestAuditDetailsUsesTrustedForwardedClientIP(t *testing.T) {
	// Given
	req := httptest.NewRequest(http.MethodPost, "/projects/42", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	var got string
	handler := requestmeta.Middleware("192.0.2.0/24")(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = buildDetails(r, http.StatusSeeOther, "update_project", "")
		}),
	)

	// When
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Then
	var details struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal([]byte(got), &details); err != nil {
		t.Fatalf("unmarshal audit details: %v", err)
	}
	if details.IP != "203.0.113.9" {
		t.Fatalf("audit IP = %q, want forwarded client", details.IP)
	}
}
