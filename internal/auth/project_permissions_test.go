package auth_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/auth"
)

func TestCanManageProject(t *testing.T) {
	for _, test := range []struct {
		name, role, membership string
		want                   bool
	}{
		{"anonymous", "", "", false},
		{"global admin", "admin", "", true},
		{"project admin", "maintainer", "admin", true},
		{"restricted project admin", "deployer", "admin", false},
		{"project deployer", "deployer", "deployer", false},
		{"viewer", "viewer", "deployer", false},
		{"nonmember", "deployer", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given: a current identity and its project membership.
			repo := newAccessTestRepo(t)
			projectID := seedProject(t, repo, "permissions")
			ctx := context.Background()
			if test.role != "" {
				user := seedUser(t, repo, "member@example.com", test.role)
				if test.membership != "" {
					addMember(t, repo, projectID, user.ID, test.membership)
				}
				req := auth.SetUser(httptest.NewRequest("GET", "/", nil), user)
				ctx = req.Context()
			}

			// When: the shared permission check runs.
			allowed := auth.CanManageProject(ctx, repo, projectID)

			// Then: only an authenticated global or project admin qualifies.
			if allowed != test.want {
				t.Fatalf("allowed = %v, want %v", allowed, test.want)
			}
		})
	}
}

func TestCanManageProjectFailsClosedOnLookupError(t *testing.T) {
	// Given: a project admin whose membership cannot be read.
	repo := newAccessTestRepo(t)
	projectID := seedProject(t, repo, "permissions")
	user := seedUser(t, repo, "member@example.com", "maintainer")
	addMember(t, repo, projectID, user.ID, "admin")
	req := auth.SetUser(httptest.NewRequest("GET", "/", nil), user)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()

	// When: authorization queries the canceled request.
	allowed := auth.CanManageProject(ctx, repo, projectID)

	// Then: an unavailable membership does not authorize management.
	if allowed {
		t.Fatal("membership lookup failure authorized project management")
	}
}
