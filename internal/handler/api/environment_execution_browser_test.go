//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestSharedEnvironmentExecutionBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	release := seedRelease(t, f.h.repo, f.project.ID)
	stage := seedEnv(t, f.h.repo)
	user := seedAPIUser(t, f.h.repo, "shared-operator@example.test", "deployer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{ProjectID: f.project.ID,
			UserID: user.ID, Role: "deployer"}); err != nil {
		t.Fatal(err)
	}
	b := startLivePackageBrowser(t, f.baseURL)
	b.setBackTestSession(t, f.baseURL, memberRoleSession(t, f.h, user.ID))
	path := fmt.Sprintf("%s/projects/%d/deploy?release_id=%d",
		f.baseURL, f.project.ID, release.ID)
	b.navigateBackTest(t, path)
	option := fmt.Sprintf(`select[name="environment_id"] option[value="%d"]`,
		f.environment.ID)
	b.wait(t, fmt.Sprintf(`!!document.querySelector(%q)`, option))
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('select[name="environment_id"]').value = %q`,
		fmt.Sprint(f.environment.ID)))
	b.screenshot(t, "shared-environment-free-floating")
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Execution eligibility"}, 201), &lifecycle)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages",
		lifecycle.ID), map[string]int64{"environment_id": stage.ID,
		"sort_order": 0}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	b.navigateBackTest(t, path)
	b.wait(t, fmt.Sprintf(
		`!document.querySelector(%q) && !!document.querySelector('select[name="environment_id"] option[value="%d"]')`,
		option,
		stage.ID,
	))
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('select[name="environment_id"]').value = %q`,
		fmt.Sprint(stage.ID)))
	b.screenshot(t, "shared-environment-lifecycle-bound")
}
