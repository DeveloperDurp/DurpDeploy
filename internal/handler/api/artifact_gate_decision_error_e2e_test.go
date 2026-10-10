//go:build e2e

package api_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestArtifactGateWebInternalErrorRecoveryE2E(t *testing.T) {
	// Given: a ready plan whose gate storage temporarily becomes unavailable.
	f, deployment, gate := newGateDeployment(t)
	if _, err := f.h.repo.DB.Exec(
		"ALTER TABLE artifact_gates RENAME TO unavailable_artifact_gates",
	); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	identity := map[string]any{"revision": gate.Revision, "sha256": gate.SHA256}
	for _, action := range []string{"approve", "reject"} {
		// When: a valid decision encounters a real database error.
		f.api(t, "POST", gateAPIPath(deployment.ID)+"/0/"+action, identity, 500)
		form := url.Values{
			"revision": {"1"}, "sha256": {gate.SHA256}, "csrf_token": {f.csrf},
		}
		request, err := http.NewRequestWithContext(t.Context(), "POST",
			f.baseURL+path+"/artifact-gates/0/"+action,
			strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := f.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		// Then: API errors retain 500; web recovery uses a fixed warning code.
		if response.StatusCode != 303 ||
			response.Header.Get("Location") != path+"?artifact_decision=error" {
			t.Fatalf("decision response=%d location=%s", response.StatusCode,
				response.Header.Get("Location"))
		}
	}
	if _, err := f.h.repo.DB.Exec(
		"ALTER TABLE unavailable_artifact_gates RENAME TO artifact_gates",
	); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.h.repo.DB.QueryRow(
		"SELECT COUNT(*) FROM audit_log WHERE action IN ('approve_artifact', 'reject_artifact')",
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed decisions audited: count=%d err=%v", count, err)
	}
	page := f.web(t, "GET", path+"?artifact_decision=error", nil, 200)
	if !strings.Contains(page, "could not be saved") ||
		strings.Contains(page, "unavailable_artifact_gates") {
		t.Fatal("internal-error recovery omitted warning or exposed internals")
	}
}
