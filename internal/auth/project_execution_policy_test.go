package auth_test

import (
	"net/http"
	"testing"
)

func TestProjectExecutionPolicy_ProjectAccessGate(t *testing.T) {
	repo := newAccessTestRepo(t)
	member := seedUser(t, repo, "policy-member@example.com", "deployer")
	nonmember := seedUser(t, repo, "policy-nonmember@example.com", "deployer")
	projectID := seedProject(t, repo, "policy-project")
	addMember(t, repo, projectID, member.ID, "deployer")
	if status := runMiddleware(t, repo, member, "1"); status != http.StatusOK {
		t.Fatalf("member status = %d, want 200", status)
	}
	if status := runMiddleware(t, repo, nonmember, "1"); status != http.StatusForbidden {
		t.Fatalf("non-member status = %d, want 403", status)
	}
}
