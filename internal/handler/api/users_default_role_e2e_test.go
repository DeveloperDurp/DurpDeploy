//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

const userRoleExplanation = "Viewer reads. Deployer runs existing releases. Maintainer authors content. Admin manages the instance."

func assertUserFormRole(t *testing.T, page []byte, want string) {
	t.Helper()
	selected := regexp.MustCompile(`<option value="([^"]+)" selected`).
		FindSubmatch(page)
	if len(selected) != 2 || string(selected[1]) != want {
		t.Fatalf("selected role = %q, want %q", selected, want)
	}
	if !strings.Contains(string(page), userRoleExplanation) {
		t.Fatal("role explanation is missing")
	}
}

func userWithRole(
	t *testing.T,
	f usersPhoneFixture,
	email, role string,
) db.User {
	t.Helper()
	for _, user := range f.listedUsers(t) {
		if user.Email == email {
			if user.Role != role {
				t.Fatalf("%s role = %q, want %q", email, user.Role, role)
			}
			return user
		}
	}
	t.Fatalf("API did not list %s", email)
	return db.User{}
}

func TestUsersDefaultRoleAPIWebE2E(t *testing.T) {
	f := newUsersPhoneFixture(t)
	assertUserFormRole(t, f.request(t, "GET", "/admin/users/new", "",
		"", f.session, false, 200), "viewer")
	for _, role := range []string{"", "invalid"} {
		body := url.Values{
			"email": {"invalid-default@example.test"}, "name": {"Invalid role"},
			"password":              {"Default-role-pass-1"},
			"password_confirmation": {"Default-role-pass-1"},
		}
		if role != "" {
			body.Set("role", role)
		}
		for _, hx := range []bool{false, true} {
			page := f.request(t, "POST", "/admin/users", body.Encode(),
				"", f.session, hx, 422)
			assertUserFormRole(t, page, "viewer")
		}
		payload := map[string]string{
			"email": "invalid-default@example.test", "name": "Invalid role",
			"password": "Default-role-pass-1",
		}
		if role != "" {
			payload["role"] = role
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		f.request(t, "POST", "/api/v1/admin/users", string(encoded),
			f.token, "", false, 400)
	}
	for _, user := range f.users {
		userWithRole(t, f, user.Email, user.Role)
		assertUserFormRole(t, f.request(t, "GET",
			fmt.Sprintf("/admin/users/%d/edit", user.ID), "", "",
			f.session, false, 200), user.Role)
	}
	for _, role := range []string{"viewer", "admin"} {
		email := "created-default-" + role + "@example.test"
		body := url.Values{
			"email": {email}, "name": {"Default role " + role}, "role": {role},
			"password":              {"Default-role-pass-1"},
			"password_confirmation": {"mismatch"},
		}
		page := f.request(t, "POST", "/admin/users", body.Encode(),
			"", f.session, false, 422)
		assertUserFormRole(t, page, role)
		body.Set("password_confirmation", "Default-role-pass-1")
		f.request(t, "POST", "/admin/users", body.Encode(),
			"", f.session, false, 200)
		userWithRole(t, f, email, role)
	}
	for _, user := range f.listedUsers(t) {
		if user.Email == "invalid-default@example.test" {
			t.Fatal("invalid or omitted role persisted a user")
		}
	}
}
