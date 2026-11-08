//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func tokenMintOwner(t *testing.T, f *artifactE2E) *db.User {
	t.Helper()
	user := seedAPIUser(t, f.h.repo, "mint-owner@example.test", "maintainer")
	_, f.token = seedAPIToken(t, f.h.repo, user.ID)
	var err error
	f.session, f.csrf, err = auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{ID: f.session, UserID: user.ID,
			CsrfToken: f.csrf, ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{ProjectID: f.project.ID,
			UserID: user.ID, Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	return user
}

type tokenMintCase struct {
	scope, role string
	projects    []int64
	allowed     bool
}

func TestTokenMintCeilingAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	owner := tokenMintOwner(t, f)
	other := seedProject(t, f.h.repo)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{ProjectID: other.ID,
			UserID: owner.ID, Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []tokenMintCase{
		{"global", "admin", nil, false},
		{"global", "maintainer", nil, true},
		{"global", "deployer", nil, true},
		{"global", "viewer", nil, true},
		{"scoped", "admin", []int64{f.project.ID}, true},
		{"scoped", "admin", []int64{f.project.ID, other.ID}, false},
		{"scoped", "maintainer", []int64{other.ID}, false},
		{"scoped", "viewer", []int64{f.project.ID, other.ID}, true},
	} {
		t.Run(tc.scope+"/"+tc.role+fmt.Sprint(tc.projects), func(t *testing.T) {
			checkTokenMintCase(t, f, tc)
		})
	}
}

func checkTokenMintCase(t *testing.T, f *artifactE2E, tc tokenMintCase) {
	t.Helper()
	before := tokenValidationState(t, f)
	flashes := tokenMintFlashCount(t, f)
	checkTokenMintAPI(t, f, tc)
	form := url.Values{"name": {"web-mint-check"},
		"scope": {tc.scope}, "role": {tc.role}}
	for _, id := range tc.projects {
		form.Add("project_ids", fmt.Sprint(id))
	}
	status := 422
	if tc.allowed {
		status = 303
	}
	web := f.web(t, "POST", "/settings/tokens", form, status)
	if tc.allowed {
		return
	}
	if !strings.Contains(web, "Token role exceeds owner permissions") ||
		tokenValidationState(t, f) != before {
		t.Fatal("rejected mint changed tokens or omitted validation")
	}
	if tokenMintFlashCount(t, f) != flashes {
		t.Fatal("rejected mint changed token flash records")
	}
}

func checkTokenMintAPI(t *testing.T, f *artifactE2E, tc tokenMintCase) {
	t.Helper()
	status := 422
	if tc.allowed {
		status = 201
	}
	request := map[string]any{
		"name": "mint-check", "scope": tc.scope, "role": tc.role,
	}
	if tc.projects != nil {
		request["project_ids"] = tc.projects
	}
	body := f.api(t, "POST", "/api/v1/tokens", request, status)
	if tc.allowed {
		return
	}
	var response struct{ Error string }
	if err := json.Unmarshal(body, &response); err != nil ||
		response.Error != "token role exceeds owner permissions" {
		t.Fatalf("ceiling error missing: %s", body)
	}
}

func tokenMintFlashCount(t *testing.T, f *artifactE2E) int {
	t.Helper()
	var count int
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM token_flash_secrets").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestTokenMintDelegationAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	tokenMintOwner(t, f)
	token := mintProjectToken(t, f, "admin", f.project.ID)
	member := seedAPIUser(
		t,
		f.h.repo,
		"delegated-member@example.test",
		"viewer",
	)
	f.token = token.ID
	f.api(t, "POST", fmt.Sprintf("/api/v1/projects/%d/members",
		f.project.ID), map[string]any{
		"user_id": member.ID, "role": "viewer",
	}, 201)
	f.api(t, "GET", "/api/v1/admin/stats", nil, 403)
}

func TestTokenMintExistingScopeAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	tokenMintOwner(t, f)
	// An owner can still edit an existing ceiling; use remains intersected.
	var existing projectToken
	if err := json.Unmarshal(f.api(t, "POST", "/api/v1/tokens",
		map[string]string{"name": "existing", "scope": "global",
			"role": "viewer"}, 201), &existing); err != nil {
		t.Fatal(err)
	}
	f.api(t, "PATCH", "/api/v1/tokens/"+existing.TokenID,
		map[string]string{"scope": "global", "role": "admin"}, 200)
	f.token = existing.ID
	f.api(t, "GET", "/api/v1/admin/stats", nil, 403)
}
