//go:build e2e

package api_test

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestProjectTokensMissingProjectsAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	before := tokenValidationState(t, f)
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		for _, includeProjects := range []bool{false, true} {
			request := map[string]any{
				"name": "missing-projects", "scope": "scoped", "role": role,
			}
			if includeProjects {
				request["project_ids"] = []int64{}
			}
			body := f.api(t, "POST", "/api/v1/tokens", request, 422)
			assertMissingTokenProjects(t, body)
		}
	}
	if after := tokenValidationState(t, f); after != before {
		t.Fatal("rejected token creation changed stored tokens")
	}
	token := mintProjectToken(t, f, "viewer", f.project.ID)
	path := "/api/v1/tokens/" + token.TokenID
	before = tokenValidationState(t, f)
	assertMissingTokenProjects(t, f.api(t, "PATCH", path, map[string]any{
		"scope": "scoped", "role": "deployer", "project_ids": []int64{},
	}, 422))
	if after := tokenValidationState(t, f); after != before {
		t.Fatal("rejected scope edit changed stored tokens")
	}
	// Other invalid fields must not be diagnosed as a missing selection.
	for _, request := range []map[string]any{
		{"scope": "scoped", "role": "root"},
		{"scope": "unknown", "role": "viewer"},
		{"role": "viewer"},
	} {
		body := f.api(t, "PATCH", path, request, 422)
		if strings.Contains(string(body), "select at least one project") {
			t.Fatal("invalid scope or role misdiagnosed as missing projects")
		}
	}
	f.api(t, "PATCH", path, map[string]any{
		"scope": "global", "role": "viewer",
	}, 200)
	metadata := f.api(t, "GET", "/api/v1/tokens", nil, 200)
	if strings.Contains(string(metadata), token.ID) {
		t.Fatal("metadata redisplayed the bearer credential")
	}
}

func tokenValidationState(t *testing.T, f *artifactE2E) string {
	t.Helper()
	// Reading the list updates the authenticating token's last-use time.
	// Compare every other field to detect writes from rejected requests.
	body := f.api(t, "GET", "/api/v1/tokens", nil, 200)
	var page map[string]json.RawMessage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	var tokens []map[string]json.RawMessage
	if err := json.Unmarshal(page["items"], &tokens); err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		delete(token, "last_used_at")
	}
	var err error
	page["items"], err = json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	stable, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	return string(stable)
}

func assertMissingTokenProjects(t *testing.T, body []byte) {
	t.Helper()
	var result struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "select at least one project" {
		t.Fatalf("missing-project error = %q", result.Error)
	}
}

func TestProjectTokensMissingProjectsAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	form := url.Values{
		"name": {"retry-selected"}, "scope": {"scoped"}, "role": {"viewer"},
	}
	before := tokenValidationState(t, f)
	body := f.web(t, "POST", "/settings/tokens", form, 422)
	assertTokenProjectFieldError(t, body, "new-token")
	if !strings.Contains(body, `value="retry-selected"`) {
		t.Fatal("validation discarded the token name")
	}
	if after := tokenValidationState(t, f); after != before {
		t.Fatal("invalid web creation stored a token")
	}
	token := mintProjectToken(t, f, "viewer", f.project.ID)
	before = tokenValidationState(t, f)
	form.Set("role", "deployer")
	body = f.web(t, "POST", "/settings/tokens/"+token.TokenID+"/scope",
		form, 422)
	assertTokenProjectFieldError(t, body, token.TokenID)
	assertTokenProjectFieldError(t, body, "mobile-"+token.TokenID)
	if strings.Contains(body, `id="new-token-projects-error"`) {
		t.Fatal("scope edit marked the unrelated create form invalid")
	}
	if after := tokenValidationState(t, f); after != before {
		t.Fatal("invalid web edit changed stored grants")
	}
	form.Set("role", "root")
	body = f.web(t, "POST", "/settings/tokens", form, 422)
	if strings.Contains(body, "Select at least one project") {
		t.Fatal("invalid role misdiagnosed as missing projects")
	}
}

func assertTokenProjectFieldError(t *testing.T, body, prefix string) {
	t.Helper()
	for _, wanted := range []string{
		`id="` + prefix + `-projects-error"`,
		`aria-describedby="` + prefix + `-help ` + prefix + `-projects-error"`,
		`aria-invalid="true"`, "Select at least one project",
	} {
		if !strings.Contains(body, wanted) {
			t.Fatalf("project selection error missing %q", wanted)
		}
	}
	if strings.Contains(body, "Token created.") ||
		regexp.MustCompile(`ddp_pat_[0-9a-f]{64}`).MatchString(body) {
		t.Fatal("validation rendered a bearer credential")
	}
}
