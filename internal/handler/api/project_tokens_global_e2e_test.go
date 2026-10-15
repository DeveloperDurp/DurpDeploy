//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectTokensGlobalRolesAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	owner := f.token
	other := seedProject(t, f.h.repo)
	member := seedAPIUser(
		t,
		f.h.repo,
		"global-role-member@example.test",
		"deployer",
	)
	for _, role := range []string{"viewer", "deployer", "admin"} {
		t.Run(role, func(t *testing.T) {
			f.token = owner
			var token projectToken
			if err := json.Unmarshal(f.api(t, "POST", "/api/v1/tokens",
				map[string]any{"name": "all-" + role, "scope": "global",
					"role": role}, 201), &token); err != nil {
				t.Fatal(err)
			}
			if token.Scope != "global" || token.Role != role ||
				len(token.ProjectIDs) != 0 {
				t.Fatal("all-project token lost its permission ceiling")
			}
			f.token = token.ID
			defer func() { f.token = owner }()
			f.api(t, "GET", f.base(), nil, 200)
			f.api(
				t,
				"GET",
				fmt.Sprintf("/api/v1/projects/%d", other.ID),
				nil,
				200,
			)
			writeStatus, adminStatus := 201, 403
			if role == "viewer" {
				writeStatus = 403
			}
			if role == "admin" {
				adminStatus = 200
			}
			f.api(t, "POST", f.base()+"/variables", map[string]string{
				"name": "GLOBAL_" + role, "value": "role ceiling",
			}, writeStatus)
			f.api(t, "GET", "/api/v1/admin/stats", nil, adminStatus)
			memberStatus := 403
			if role == "admin" {
				memberStatus = 201
			}
			f.api(t, "POST", f.base()+"/members", map[string]any{
				"user_id": member.ID, "role": "deployer",
			}, memberStatus)
			// A ceiling cannot mint or edit a token to recover owner permissions.
			f.api(t, "GET", "/api/v1/tokens", nil, 403)
			f.api(t, "POST", "/api/v1/tokens", map[string]string{
				"name": "escape",
			}, 403)
			f.api(t, "PATCH", "/api/v1/tokens/"+token.TokenID,
				map[string]string{"scope": "global"}, 403)
			f.api(t, "DELETE", "/api/v1/tokens/"+token.TokenID, nil, 403)
			f.api(t, "GET", "/api/v1/admin/tokens", nil, 403)
			f.api(t, "DELETE", "/api/v1/admin/tokens/"+token.TokenID, nil, 403)
			// The owner can change a global ceiling without replacing the bearer.
			f.token = owner
			f.api(t, "PATCH", "/api/v1/tokens/"+token.TokenID,
				map[string]string{"scope": "global", "role": "viewer"}, 200)
			f.token = token.ID
			f.api(t, "POST", f.base()+"/variables", map[string]string{
				"name": "AFTER_EDIT_" + role, "value": "denied",
			}, 403)
		})
	}
}

func TestProjectTokensGlobalOwnerAccessAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	admin := f.token
	user := seedAPIUser(t, f.h.repo, "global-owner@example.test", "admin")
	_, owner := seedAPIToken(t, f.h.repo, user.ID)
	f.token = owner
	var token projectToken
	if err := json.Unmarshal(f.api(t, "POST", "/api/v1/tokens",
		map[string]string{"name": "all-admin", "scope": "global",
			"role": "admin"}, 201), &token); err != nil {
		t.Fatal(err)
	}
	f.token = token.ID
	f.api(t, "GET", f.base(), nil, 200)
	f.token = admin
	f.api(t, "POST", f.base()+"/members", map[string]any{
		"user_id": user.ID, "role": "admin",
	}, 201)
	for _, role := range []string{"deployer", "viewer"} {
		f.token = admin
		f.api(t, "PUT", fmt.Sprintf("/api/v1/admin/users/%d", user.ID),
			map[string]string{"email": user.Email, "name": user.Name,
				"role": role}, 200)
		f.token = token.ID
		f.api(t, "GET", "/api/v1/admin/stats", nil, 403)
		f.api(t, "GET", f.base(), nil, 200)
		status := 201
		if role == "viewer" {
			status = 403
		}
		f.api(t, "POST", f.base()+"/variables", map[string]string{
			"name": "OWNER_" + role, "value": "owner ceiling",
		}, status)
	}
	f.token = admin
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("%s/members/%d", f.base(), user.ID),
		nil,
		204,
	)
	f.token = token.ID
	f.api(t, "GET", f.base(), nil, 403)
	// Invalid persisted ceilings must fail authentication.
	if _, err := f.h.repo.Queries.SetApiTokenScope(t.Context(),
		db.SetApiTokenScopeParams{ID: token.TokenID, Scope: "scoped",
			TokenRole: "admin"}); err != nil {
		t.Fatal(err)
	}
	f.api(t, "GET", "/api/v1/projects", nil, 401)
}
