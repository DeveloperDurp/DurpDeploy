//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type releaseAvailabilityFixture struct {
	*artifactE2E
	old, latest db.Release
	nextEnv     int64
}

func newReleaseAvailabilityFixture(t *testing.T) releaseAvailabilityFixture {
	t.Helper()
	f := newProjectGroupsE2E(t)
	f.project = seedProject(t, f.h.repo)
	f.environment = seedEnv(t, f.h.repo)
	var next, lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/environments",
		map[string]string{"name": "Production"}, 201), &next)
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Release availability"}, 201), &lifecycle)
	path := fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID)
	f.api(t, "POST", path, map[string]any{
		"environment_id": f.environment.ID,
	}, 201)
	f.api(t, "POST", path, map[string]any{
		"environment_id": next.ID, "requires_approval": true,
	}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	old := seedRelease(t, f.h.repo, f.project.ID)
	latest := seedRelease(t, f.h.repo, f.project.ID)
	// Equal timestamps exercise deterministic ordering by release ID.
	if _, err := f.h.repo.DB.Exec(
		"UPDATE releases SET created_at = 100 WHERE project_id = ?",
		f.project.ID,
	); err != nil {
		t.Fatal(err)
	}
	seedDeployment(t, f.h.repo, old.ID, f.environment.ID, "succeeded")
	seedDeployment(t, f.h.repo, old.ID, next.ID, "succeeded")
	return releaseAvailabilityFixture{f, old, latest, next.ID}
}

func (f releaseAvailabilityFixture) availability(
	t *testing.T,
) repository.ProjectReleaseAvailability {
	t.Helper()
	var response struct {
		State repository.ProjectReleaseAvailability `json:"release_availability"`
	}
	decodeLifecycleTest(t, f.api(t, "GET", f.base(), nil, 200), &response)
	return response.State
}

func (f releaseAvailabilityFixture) checkAvailability(
	t *testing.T,
	want bool,
) repository.ProjectReleaseAvailability {
	t.Helper()
	state := f.availability(t)
	if state.UpdatesAvailable != want ||
		state.LatestRelease.ID != f.latest.ID {
		t.Fatalf("availability: %+v", state)
	}
	var page struct {
		Items []struct{ ID int64 }
		Total int64
	}
	decodeLifecycleTest(t, f.api(t, "GET",
		"/api/v1/projects?updates_available=true&limit=1", nil, 200), &page)
	if (page.Total == 1) != want || (len(page.Items) == 1) != want {
		t.Fatalf("filtered page: %+v", page)
	}
	body := f.web(t, "GET", "/projects?updates_available=true", nil, 200)
	if strings.Contains(string(body), "data-release-update") != want {
		t.Fatal("web filter disagrees with API")
	}
	return state
}

func TestReleaseAvailabilityAPIWebE2E(t *testing.T) {
	f := newReleaseAvailabilityFixture(t)
	state := f.checkAvailability(t, true)
	if !state.Environments[0].ReadyToDeploy ||
		state.Environments[1].ReadyToDeploy ||
		!state.Environments[1].RequiresApproval {
		t.Fatalf("promotion readiness: %+v", state.Environments)
	}
	for _, status := range []string{"failed", "cancelled", "running"} {
		seedDeployment(t, f.h.repo, f.latest.ID, f.environment.ID, status)
		f.checkAvailability(t, true)
	}
	dry := seedDeployment(
		t,
		f.h.repo,
		f.latest.ID,
		f.environment.ID,
		"succeeded",
	)
	if _, err := f.h.repo.DB.Exec(
		"UPDATE deployments SET ansible_check_mode = 1 WHERE id = ?", dry.ID,
	); err != nil {
		t.Fatal(err)
	}
	state = f.checkAvailability(t, true)
	if state.Environments[1].ReadyToDeploy ||
		state.Environments[0].CurrentRelease.ID != f.old.ID {
		t.Fatal("check run became a successful deployment")
	}
	seedDeployment(t, f.h.repo, f.latest.ID, f.environment.ID, "succeeded")
	state = f.checkAvailability(t, true)
	if !state.Environments[1].ReadyToDeploy {
		t.Fatal("successful preceding stage did not enable promotion")
	}
	seedDeployment(t, f.h.repo, f.latest.ID, f.nextEnv, "succeeded")
	f.checkAvailability(t, false)
	seedDeployment(t, f.h.repo, f.old.ID, f.nextEnv, "succeeded")
	f.checkAvailability(t, true)
	f.api(t, "GET", "/api/v1/projects?updates_available=garbage", nil, 400)
	f.web(t, "GET", "/projects?updates_available=garbage", nil, 400)
	// Filtering occurs before pagination.
	var page struct {
		Total int64
		Items []any
	}
	decodeLifecycleTest(t, f.api(t, "GET",
		"/api/v1/projects?updates_available=true&offset=1", nil, 200), &page)
	if page.Total != 1 || len(page.Items) != 0 {
		t.Fatalf("pagination lost filtered total: %+v", page)
	}
	// Visibility applies before filtering and before availability is returned.
	viewer := seedAPIUser(t, f.h.repo, "release-viewer@test.local", "viewer")
	_, f.token = seedAPIToken(t, f.h.repo, viewer.ID)
	f.session = memberRoleSession(t, f.h, viewer.ID)
	f.api(t, "GET", f.base(), nil, 403)
	decodeLifecycleTest(t, f.api(t, "GET",
		"/api/v1/projects?updates_available=true", nil, 200), &page)
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("update filter leaked an inaccessible project")
	}
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: viewer.ID, Role: "viewer",
		}); err != nil {
		t.Fatal(err)
	}
	state = f.availability(t)
	if !state.UpdatesAvailable {
		t.Fatal("project viewer cannot read release availability")
	}
	body := f.web(t, "GET", fmt.Sprintf("/projects/%d", f.project.ID), nil, 200)
	if !strings.Contains(string(body), "data-release-update") ||
		strings.Contains(string(body), ">Deploy</a>") {
		t.Fatal("viewer release status is missing or exposes a deploy action")
	}
}
