//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

type usersPhoneFixture struct {
	permissionFixture
	adminID        int64
	token, session string
	users          []db.User
}

func newUsersPhoneFixture(t *testing.T) usersPhoneFixture {
	t.Helper()
	f := usersPhoneFixture{permissionFixture: newPermissionFixture(t)}
	admin := seedAPIUser(t, f.h.repo, "phone-admin@example.test", "admin")
	f.adminID = admin.ID
	_, f.token = seedAPIToken(t, f.h.repo, admin.ID)
	f.session = memberRoleSession(t, f.h, admin.ID)
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		body, err := json.Marshal(map[string]string{
			"email": "alexandra.long.identity.for.mobile." + role + "@example.usability.test",
			"name":  "Alexandra" + strings.Repeat("LongName", 10) + " " + role,
			"role":  role, "password": "Phone-layout-pass-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		var user db.User
		decodeStepLogTest(t, f.request(t, "POST", "/api/v1/admin/users",
			string(body), f.token, "", false, 201), &user)
		f.users = append(f.users, user)
	}
	return f
}

func (f usersPhoneFixture) listedUsers(t *testing.T) []db.User {
	t.Helper()
	var result struct {
		Items []db.User `json:"items"`
	}
	decodeStepLogTest(t, f.request(t, "GET", "/api/v1/admin/users",
		"", f.token, "", false, 200), &result)
	return result.Items
}

func TestUsersPhoneAPIWebE2E(t *testing.T) {
	f := newUsersPhoneFixture(t)
	listed := f.listedUsers(t)
	page := string(f.request(t, "GET", "/admin/users", "", "",
		f.session, false, 200))
	for _, want := range f.users {
		found := false
		for _, user := range listed {
			found = found || user.ID == want.ID && user.Email == want.Email &&
				user.Name == want.Name && user.Role == want.Role
		}
		if !found || !strings.Contains(page, html.EscapeString(want.Email)) ||
			!strings.Contains(page, html.EscapeString(want.Name)) {
			t.Fatalf("API/web lost identity %d", want.ID)
		}
	}
	viewer := f.users[0]
	_, token := seedAPIToken(t, f.h.repo, viewer.ID)
	f.request(t, "GET", "/api/v1/admin/users", "", token, "", false, 403)
	f.request(t, "GET", "/admin/users", "", "",
		memberRoleSession(t, f.h, viewer.ID), false, 403)
	if strings.Contains(
		page,
		fmt.Sprintf(`hx-delete="/admin/users/%d"`, f.adminID),
	) {
		t.Fatal("self-delete is exposed")
	}
}
