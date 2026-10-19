//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type auditIdentityFixture struct {
	permissionFixture
	actor, target  *db.User
	token, session string
}

func newAuditIdentityFixture(t *testing.T) auditIdentityFixture {
	t.Helper()
	f := auditIdentityFixture{permissionFixture: newPermissionFixture(t)}
	f.actor = seedAPIUser(t, f.h.repo, "operator@audit.test", "admin")
	f.actor.Name = "Audit Operator <Élodie> 100%"
	if err := f.h.repo.Queries.UpdateUser(t.Context(), db.UpdateUserParams{
		ID: f.actor.ID, Name: f.actor.Name, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	f.target = seedAPIUser(t, f.h.repo, "target@audit.test", "viewer")
	_, f.token = seedAPIToken(t, f.h.repo, f.actor.ID)
	f.session = memberRoleSession(t, f.h, f.actor.ID)
	f.request(t, "PUT", fmt.Sprintf("/api/v1/admin/users/%d", f.target.ID),
		`{"name":"Audit Target","role":"viewer"}`, f.token, "", false, 200)
	return f
}

func (f auditIdentityFixture) entries(
	t *testing.T, query string,
) []repository.AuditLogEntry {
	t.Helper()
	var result struct{ Items []repository.AuditLogEntry }
	body := f.request(t, "GET", "/api/v1/admin/audit?"+query,
		"", f.token, "", false, 200)
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result.Items
}

func TestAuditIdentitiesAPIWebE2E(t *testing.T) {
	f := newAuditIdentityFixture(t)
	for _, user := range []string{strings.ToUpper(f.actor.Email), f.actor.Name} {
		checkAuditIdentityFilter(t, f, user)
	}
	checkAuditIdentityEdges(t, f)
	checkAuditIdentityPermissions(t, f)
	checkAuditIdentityMissingActor(t, f)
	checkAuditAmbiguousTargets(t)
}

func checkAuditIdentityFilter(
	t *testing.T,
	f auditIdentityFixture,
	user string,
) {
	t.Helper()
	query := url.Values{
		"user": {user}, "action": {"update_user"}, "entity_type": {"user"},
		"user_id": {fmt.Sprint(f.actor.ID)}, "limit": {"1"},
	}.Encode()
	entries := f.entries(t, query)
	if len(entries) != 1 {
		t.Fatalf("identity filter returned %d entries", len(entries))
	}
	entry := entries[0]
	if entry.ActorName != f.actor.Name ||
		entry.ActorEmail != f.actor.Email ||
		entry.UserID.Int64 != f.actor.ID ||
		entry.EntityID.Int64 != f.target.ID ||
		entry.TargetURL != fmt.Sprintf("/admin/users/%d/edit", f.target.ID) {
		t.Fatalf("identity or stable IDs lost: %+v", entry)
	}
	page := f.request(t, "GET", "/admin/audit?"+query,
		"", "", f.session, false, 200)
	if !strings.Contains(
		string(page),
		"Audit Operator &lt;Élodie&gt; 100%",
	) ||
		!strings.Contains(string(page), entry.TargetURL) {
		t.Fatal("web result lost the escaped identity or target link")
	}

}

func checkAuditIdentityEdges(t *testing.T, f auditIdentityFixture) {
	t.Helper()
	for _, query := range []string{"user=%25", "user=operator", "user=missing",
		"user=operator%40audit.test&user_id=99999", "user=%27+OR+1%3D1--"} {
		if entries := f.entries(t, query); len(entries) != 0 {
			t.Fatalf(
				"exact identity filter %s matched unexpected entries",
				query,
			)
		}
	}
	f.request(t, "DELETE", fmt.Sprintf("/api/v1/admin/users/%d", f.target.ID),
		"", f.token, "", false, 204)
	entries := f.entries(t, "action=update_user")
	if len(entries) != 1 || entries[0].EntityID.Int64 != f.target.ID ||
		entries[0].TargetURL != "" {
		t.Fatalf(
			"deleted target must retain its ID without a link: %+v",
			entries,
		)
	}
}

func checkAuditIdentityPermissions(t *testing.T, f auditIdentityFixture) {
	t.Helper()
	for _, role := range []string{"viewer", "deployer", "maintainer"} {
		user := seedAPIUser(t, f.h.repo, role+"@audit.test", role)
		_, token := seedAPIToken(t, f.h.repo, user.ID)
		f.request(t, "GET", "/api/v1/admin/audit", "", token, "", false, 403)
		f.request(t, "GET", "/admin/audit", "", "",
			memberRoleSession(t, f.h, user.ID), false, 403)
	}
	f.request(t, "GET", "/api/v1/admin/audit", "", "", "", false, 401)
}

func checkAuditIdentityMissingActor(t *testing.T, f auditIdentityFixture) {
	t.Helper()
	if _, err := f.h.repo.Queries.CreateAuditLog(t.Context(),
		db.CreateAuditLogParams{
			Action: "system_audit", EntityType: "unknown",
			EntityID: sql.NullInt64{Int64: f.target.ID, Valid: true},
			Details:  sql.NullString{String: `{"path":"https://evil.test"}`, Valid: true},
		}); err != nil {
		t.Fatal(err)
	}
	entries := f.entries(t, "action=system_audit")
	if len(entries) != 1 || entries[0].UserID.Valid ||
		entries[0].ActorName != "" || entries[0].ActorEmail != "" ||
		entries[0].TargetURL != "" {
		t.Fatalf("missing actor or unsupported target: %+v", entries)
	}
	page := f.request(t, "GET", "/admin/audit?action=system_audit",
		"", "", f.session, false, 200)
	if !strings.Contains(string(page), "No user ID") ||
		strings.Contains(string(page), `href="https://evil.test"`) {
		t.Fatal("missing actor fallback or target validation failed")
	}
}
