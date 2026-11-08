//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestGlobalVisibilityBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	viewer := seedAPIUser(
		t,
		f.h.repo,
		"visibility-viewer@example.test",
		"viewer",
	)
	manager := seedAPIUser(
		t,
		f.h.repo,
		"visibility-manager@example.test",
		"maintainer",
	)
	target := seedAPIUser(
		t,
		f.h.repo,
		"visibility-target@example.test",
		"deployer",
	)
	for _, member := range []struct {
		id   int64
		role string
	}{{viewer.ID, "viewer"}, {manager.ID, "admin"}} {
		if err := f.h.repo.Queries.AddProjectMember(t.Context(),
			db.AddProjectMemberParams{ProjectID: f.project.ID,
				UserID: member.id, Role: member.role},
		); err != nil {
			t.Fatal(err)
		}
	}
	b := startLivePackageBrowser(t, f.baseURL)
	b.setBackTestSession(t, f.baseURL, memberRoleSession(t, f.h, viewer.ID))
	b.navigateBackTest(t, f.baseURL+"/environments")
	b.wait(
		t,
		fmt.Sprintf(
			"document.body.textContent.includes(%q)",
			f.environment.Name,
		),
	)
	b.screenshot(t, "global-environment-visibility")
	edit := fmt.Sprintf("%s/projects/%d/edit", f.baseURL, f.project.ID)
	b.navigateBackTest(t, edit)
	b.wait(
		t,
		fmt.Sprintf(
			`document.querySelector('#project-members')?.textContent.includes(%q) && !document.querySelector('select[name="user_id"]')`,
			manager.Email,
		),
	)
	b.screenshot(t, "project-roster-visibility")
	b.setBackTestSession(t, f.baseURL, memberRoleSession(t, f.h, manager.ID))
	b.navigateBackTest(t, edit)
	b.wait(
		t,
		fmt.Sprintf(
			`document.querySelector('select[name="user_id"]')?.textContent.includes(%q)`,
			target.Email,
		),
	)
	b.screenshot(t, "global-directory-picker")
}
