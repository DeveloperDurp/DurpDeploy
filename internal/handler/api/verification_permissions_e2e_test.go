//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestVerificationAdminConfigurationE2E(t *testing.T) {
	// Given: an admin configures a shared environment's Bash check.
	f := newVerificationE2E(t)
	configureVerification(t, f, "bash", "echo configured-check", 5)
	adminToken := f.token
	deployer := seedAPIUser(
		t,
		f.h.repo,
		"outsider@verification.example",
		"deployer",
	)
	_, f.token = seedAPIToken(t, f.h.repo, deployer.ID)
	path := fmt.Sprintf("/api/v1/environments/%d", f.environment.ID)
	assertRedacted := func(response []byte) {
		t.Helper()
		if strings.Contains(string(response), "verification_target") ||
			strings.Contains(string(response), "configured-check") {
			t.Fatalf("non-admin received verification target: %s", response)
		}
	}
	for _, role := range []string{"deployer", "viewer"} {
		user := seedAPIUser(t, f.h.repo, role+"-read@example.test", role)
		_, f.token = seedAPIToken(t, f.h.repo, user.ID)
		assertRedacted(f.api(t, "GET", path, nil, 200))
		assertRedacted(f.api(t, "GET", "/api/v1/environments", nil, 200))
	}
	_, f.token = seedAPIToken(t, f.h.repo, deployer.ID)

	// When: a deployer tries to replace or disable the check through either surface.
	for _, kind := range []string{"bash", ""} {
		f.api(t, "PUT", path, map[string]any{
			"name":                f.environment.Name,
			"verification_type":   kind,
			"verification_target": "echo injected",
		}, 403)
	}
	f.api(t, "POST", "/api/v1/environments", map[string]string{
		"name":                "Injected",
		"verification_type":   "bash",
		"verification_target": "echo injected",
	}, 403)
	if _, err := f.h.repo.Queries.CreateSession(
		t.Context(),
		db.CreateSessionParams{
			ID:        "verification-deployer-session",
			UserID:    deployer.ID,
			CsrfToken: f.csrf,
			ExpiresAt: 4102444800,
		},
	); err != nil {
		t.Fatal(err)
	}
	f.session = "verification-deployer-session"
	query := "?verification_type=bash&verification_target=echo+injected"
	f.web(
		t,
		"POST",
		"/environments"+query,
		url.Values{"name": {"injected-query"}},
		403,
	)
	webPath := fmt.Sprintf("/environments/%d", f.environment.ID)
	f.web(
		t,
		"PUT",
		webPath+query,
		url.Values{"name": {f.environment.Name}},
		403,
	)
	for _, kind := range []string{"bash", ""} {
		f.web(t, "PUT", webPath, url.Values{
			"name": {f.environment.Name},
			"verification_type": {
				kind,
			},
			"verification_target": {"echo injected"},
		}, 403)
	}
	page := f.web(t, "GET", webPath+"/edit", nil, 200)
	if strings.Contains(page, `name="verification_target"`) {
		t.Fatal("deployer sees configuration form")
	}

	// Then: ordinary edits preserve the admin's check atomically.
	assertRedacted(f.api(
		t,
		"PUT",
		path,
		map[string]string{"name": f.environment.Name, "description": "edited"},
		200,
	))
	f.web(
		t,
		"PUT",
		webPath,
		url.Values{
			"name":        {f.environment.Name},
			"description": {"edited again"},
		},
		303,
	)
	f.token = adminToken
	stored := string(f.api(t, "GET", path, nil, 200))
	if !strings.Contains(stored, "configured-check") {
		t.Fatalf("configuration changed: %s", stored)
	}
	for _, body := range []map[string]any{
		{"verification_type": "unknown"},
		{"verification_type": "http", "verification_target": "http://127.0.0.1/private"},
		{"verification_type": "http", "verification_target": "file:///etc/passwd"},
		{"verification_type": "http", "verification_target": "https://user:pass@example.com"},
		{"verification_type": "bash", "verification_target": " "},
		{"verification_timeout_seconds": -1},
		{"verification_timeout_seconds": 0},
		{"verification_timeout_seconds": 301},
	} {
		body["name"] = f.environment.Name
		f.api(t, "PUT", path, body, 422)
	}
}
