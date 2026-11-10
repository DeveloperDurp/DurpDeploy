//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestSharedEnvironmentExecutionAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	foreign := seedProject(t, f.h.repo)
	foreignRelease := seedRelease(t, f.h.repo, foreign.ID)
	seedDeployment(t, f.h.repo, foreignRelease.ID, f.environment.ID,
		"succeeded")
	release := seedRelease(t, f.h.repo, f.project.ID)
	path := fmt.Sprintf("/projects/%d/deploy", f.project.ID)
	body := map[string]int64{"release_id": release.ID,
		"environment_id": f.environment.ID}
	for _, scenario := range []struct {
		name, global, member, ceiling string
		apiStatus, webStatus          int
	}{
		{"deployer", "deployer", "deployer", "", 201, 303},
		{"maintainer", "maintainer", "maintainer", "", 201, 303},
		{"project-viewer", "maintainer", "viewer", "", 403, 403},
		{"global-viewer", "viewer", "maintainer", "", 403, 403},
		{"nonmember", "maintainer", "", "", 403, 403},
		{"scoped-deployer", "maintainer", "maintainer", "deployer", 201, 303},
		{"scoped-viewer", "maintainer", "maintainer", "viewer", 403, 303},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			user := seedAPIUser(t, f.h.repo,
				scenario.name+"@environment.test", scenario.global)
			if scenario.member != "" {
				require.NoError(
					t,
					f.h.repo.Queries.AddProjectMember(t.Context(),
						db.AddProjectMemberParams{ProjectID: f.project.ID,
							UserID: user.ID, Role: scenario.member}),
				)
			}
			_, f.token = seedAPIToken(t, f.h.repo, user.ID)
			f.session = memberRoleSession(t, f.h, user.ID)
			f.csrf = "member-role-csrf"
			if scenario.ceiling != "" {
				f.token = mintProjectToken(t, f, scenario.ceiling,
					f.project.ID).ID
			}
			// The target environment is shared. Its other project stays private.
			f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", foreign.ID),
				nil, 403)
			before, err := f.h.repo.Queries.ListDeploymentsByRelease(
				t.Context(), release.ID)
			require.NoError(t, err)
			f.api(t, "POST", f.base()+"/deployments", body,
				scenario.apiStatus)
			f.web(t, "POST", path, url.Values{
				"release_id":     {fmt.Sprint(release.ID)},
				"environment_id": {fmt.Sprint(f.environment.ID)},
			}, scenario.webStatus)
			after, err := f.h.repo.Queries.ListDeploymentsByRelease(
				t.Context(), release.ID)
			require.NoError(t, err)
			want := len(before)
			if scenario.apiStatus == 201 {
				want++
			}
			if scenario.webStatus == 303 {
				want++
			}
			require.Len(t, after, want)
		})
	}
}

func TestSharedEnvironmentLifecycleAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	release := seedRelease(t, f.h.repo, f.project.ID)
	stage := seedEnv(t, f.h.repo)
	path := fmt.Sprintf("/projects/%d/deploy?release_id=%d",
		f.project.ID, release.ID)
	page := f.web(t, "GET", path, nil, 200)
	selects := dashboardElements(dashboardDocument(t, page),
		"name", "environment_id")
	require.Len(t, selects, 1)
	require.Len(t, dashboardElements(selects[0], "value",
		fmt.Sprint(f.environment.ID)), 1)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Execution eligibility"}, 201), &lifecycle)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages",
		lifecycle.ID), map[string]int64{"environment_id": stage.ID,
		"sort_order": 0}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	page = f.web(t, "GET", path, nil, 200)
	selects = dashboardElements(dashboardDocument(t, page),
		"name", "environment_id")
	require.Len(t, selects, 1)
	require.Empty(t, dashboardElements(selects[0], "value",
		fmt.Sprint(f.environment.ID)))
	require.Len(t, dashboardElements(selects[0], "value",
		fmt.Sprint(stage.ID)), 1)
	f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 422)
	for _, force := range []string{"false", "true"} {
		f.web(t, "POST", fmt.Sprintf("/projects/%d/deploy", f.project.ID),
			url.Values{"release_id": {fmt.Sprint(release.ID)},
				"environment_id": {fmt.Sprint(f.environment.ID)},
				"force":          {force}}, 422)
	}
	deps, err := f.h.repo.Queries.ListDeploymentsByRelease(
		t.Context(), release.ID)
	require.NoError(t, err)
	require.Empty(t, deps)
	f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": stage.ID,
	}, 201)
}

func TestSharedEnvironmentVariableCaptureAPIE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	foreign := seedProject(t, f.h.repo)
	foreignPath := fmt.Sprintf("/api/v1/projects/%d", foreign.ID)
	f.api(t, "POST", foreignPath+"/variables", map[string]any{
		"name": "FOREIGN_ONLY", "value": "foreign-private",
		"environment_id": f.environment.ID, "secret": true,
	}, 201)
	f.api(t, "POST", f.base()+"/variables", map[string]any{
		"name": "SOURCE_SCOPE", "value": "source-selected",
		"environment_id": f.environment.ID,
	}, 201)
	var release db.Release
	decodeLifecycleTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "shared-environment"}, 201), &release)
	var deployment db.Deployment
	decodeLifecycleTest(t, f.api(t, "POST", f.base()+"/deployments",
		map[string]int64{"release_id": release.ID,
			"environment_id": f.environment.ID}, 201), &deployment)
	variables, err := f.h.repo.ListDeploymentVariables(
		t.Context(),
		deployment.ID,
	)
	require.NoError(t, err)
	found := false
	for _, variable := range variables {
		require.NotEqual(t, "FOREIGN_ONLY", variable.Name)
		require.False(
			t,
			strings.Contains(variable.Value.String, "foreign-private"),
		)
		if variable.Name == "SOURCE_SCOPE" {
			found = true
			require.Equal(t, "source-selected", variable.Value.String)
			require.Equal(t, f.environment.ID, variable.EnvironmentID.Int64)
		}
	}
	require.True(t, found, "source environment scope was not captured")
}
