//go:build e2e

package api_test

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func rotationAPIVariable(
	t *testing.T,
	f *artifactE2E,
	base, name, value string,
) int64 {
	t.Helper()
	var row struct{ ID int64 }
	decodeLifecycleTest(t, f.api(
		t,
		"POST",
		base+"/credentials",
		map[string]any{
			"name":  name,
			"value": value,
		},
		201,
	), &row)
	return row.ID
}

func TestCredentialRotationAllReleasesAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	id := rotationAPIVariable(t, f, f.base(), "ACCESS_TOKEN", "original-token")
	before, err := f.h.repo.Queries.GetVariable(t.Context(), id)
	require.NoError(t, err)
	step := map[string]string{
		"name": "Check rotated credential",
		"script_body": `test "$(printf '%s' "$ACCESS_TOKEN" | sha256sum | cut -d' ' -f1)" = ` + fmt.Sprintf(
			"%x",
			sha256.Sum256([]byte("replacement-token")),
		) + ` || exit 1; printf 'rotation-verified\n'`,
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}
	f.api(t, "POST", f.base()+"/steps", step, 201)
	releases := []db.Release{
		verificationRelease(t, f, "rotation-first"),
		verificationRelease(t, f, "rotation-second"),
	}
	original := verificationDeploy(t, f, releases[0])
	f.completion(t, original.ID, events.DeploymentFailed)
	var saved, updated deletionRunbook
	bookInput := map[string]any{
		"name": "Rotating runbook secret", "steps": []map[string]string{step},
	}
	decodeLifecycleTest(t,
		f.api(t, "POST", f.base()+"/runbooks", bookInput, 201), &saved)
	bookPath := fmt.Sprintf("%s/runbooks/%d", f.base(), saved.Runbook.ID)
	decodeLifecycleTest(t,
		f.api(t, "PUT", bookPath, bookInput, 201), &updated)
	path := fmt.Sprintf("%s/credentials/variable/%d", f.base(), id)
	expires := time.Now().Add(10 * 24 * time.Hour).Unix()
	var result repository.CredentialRotationResult
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"PUT",
			path,
			map[string]any{"expires_at": expires, "revision": 0},
			200,
		),
		&result,
	)
	require.Equal(t, "due_soon", result.Credential.Status)
	require.Zero(t, result.Credential.RotatedAt)
	require.Zero(t, result.ReleaseVariablesUpdated)
	after, err := f.h.repo.Queries.GetVariable(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, before.Value, after.Value)
	decodeLifecycleTest(t, f.api(t, "PUT", path, map[string]any{
		"value": "replacement-token", "expires_at": expires, "revision": 1,
	}, 200), &result)
	require.EqualValues(t, 4, result.ReleaseVariablesUpdated)
	require.Positive(t, result.Credential.RotatedAt)
	for _, release := range releases {
		deployment := verificationDeploy(t, f, release)
		f.completion(t, deployment.ID, events.DeploymentSucceeded)
		logs := f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
			nil,
			200,
		)
		require.Contains(t, string(logs), "rotation-verified")
		require.NotContains(t, string(logs), "replacement-token")
		require.NotContains(
			t,
			string(
				f.api(
					t,
					"GET",
					fmt.Sprintf("%s/releases/%d", f.base(), release.ID),
					nil,
					200,
				),
			),
			"replacement-token",
		)
	}
	for _, version := range []db.RunbookVersion{saved.Version, updated.Version} {
		var execution db.RunbookExecution
		decodeLifecycleTest(t,
			f.api(t, "POST", bookPath+"/executions", map[string]int64{
				"environment_id": f.environment.ID,
				"version_id":     version.ID,
			}, 201), &execution)
		f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
		logs := f.api(t, "GET", fmt.Sprintf(
			"%s/runbook-executions/%d/logs", f.base(), execution.ID), nil, 200)
		require.Contains(t, string(logs), "rotation-verified")
		require.NotContains(t, string(logs), "replacement-token")
	}
	f.api(t, "POST", path+"/rotate", map[string]any{"revision": 2}, 422)
	f.api(
		t,
		"POST",
		path+"/rotate",
		map[string]any{"value": "stale-value", "revision": 1},
		409,
	)
	f.api(t, "PUT", path, map[string]any{"expires_at": -1, "revision": 2}, 422)
	rows := f.api(t, "GET", f.base()+"/credentials", nil, 200)
	require.NotContains(t, string(rows), "replacement-token")
	webPath := strings.TrimPrefix(path, "/api/v1")
	f.web(t, "POST", webPath, url.Values{"revision": {"2"}}, 303)
	after, err = f.h.repo.Queries.GetVariable(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, result.Credential.RotatedAt, after.RotatedAt)
	require.EqualValues(t, 3, after.RotationRevision)
	page := f.web(
		t,
		"POST",
		webPath,
		url.Values{"revision": {"3"}, "value": {"   "}},
		422,
	)
	require.Contains(t, page, "nonempty replacement")
	require.NotContains(t, page, "replacement-token")
	var success, failure int
	require.NoError(
		t,
		f.h.repo.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action = 'rotate_credential'").
			Scan(&success),
	)
	require.NoError(
		t,
		f.h.repo.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action = 'rotate_credential_failed'").
			Scan(&failure),
	)
	require.Equal(t, 1, success)
	require.Equal(t, 3, failure)
	var leaks int
	require.NoError(
		t,
		f.h.repo.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE details LIKE '%replacement-token%' OR details LIKE '%stale-value%'").
			Scan(&leaks),
	)
	require.Zero(t, leaks)
}

func TestCredentialRotationLifecycleAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var lc struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Rotating shared secrets"},
			201,
		),
		&lc,
	)
	base := fmt.Sprintf("/api/v1/lifecycles/%d", lc.ID)
	f.api(
		t,
		"POST",
		base+"/stages",
		map[string]int64{"environment_id": f.environment.ID},
		201,
	)
	id := rotationAPIVariable(t, f, base, "SHARED_TOKEN", "original-shared")
	projects := []db.Project{f.project, seedProject(t, f.h.repo)}
	var releases []db.Release
	for i, project := range projects {
		projectBase := fmt.Sprintf("/api/v1/projects/%d", project.ID)
		f.api(
			t,
			"PUT",
			projectBase,
			map[string]any{"name": project.Name, "lifecycle_id": lc.ID},
			200,
		)
		expected := "rotated-shared"
		if i == 1 {
			rotationAPIVariable(
				t,
				f,
				projectBase,
				"SHARED_TOKEN",
				"project-override",
			)
			expected = "project-override"
		}
		f.api(t, "POST", projectBase+"/steps", map[string]string{
			"name": "Check shared secret",
			"script_body": fmt.Sprintf(
				`test "$SHARED_TOKEN" = %s || exit 1; printf 'shared-verified\n'`,
				expected,
			),
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		}, 201)
		var release db.Release
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				projectBase+"/releases",
				map[string]string{"version": "before-shared-rotation"},
				201,
			),
			&release,
		)
		releases = append(releases, release)
	}
	original := verificationDeploy(t, f, releases[0])
	f.completion(t, original.ID, events.DeploymentFailed)
	path := fmt.Sprintf("%s/credentials/lifecycle/%d", base, id)
	f.api(
		t,
		"PUT",
		path,
		map[string]any{"rotation_due_at": time.Now().Add(-time.Hour).Unix()},
		200,
	)
	require.Contains(
		t,
		string(f.api(t, "GET", base+"/credentials", nil, 200)),
		`"status":"due"`,
	)
	f.api(
		t,
		"PUT",
		path,
		map[string]any{"value": "rotated-shared", "revision": 1},
		200,
	)
	for i, release := range releases {
		var deployment db.Deployment
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				fmt.Sprintf("/api/v1/projects/%d/deployments", projects[i].ID),
				map[string]int64{
					"release_id":     release.ID,
					"environment_id": f.environment.ID,
				},
				201,
			),
			&deployment,
		)
		f.completion(t, deployment.ID, events.DeploymentSucceeded)
	}
}

func TestCredentialRotationPermissionsAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	id := rotationAPIVariable(t, f, f.base(), "PROJECT_TOKEN", "original")
	owner := f.token
	other := seedProject(t, f.h.repo)
	var lc struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Unrelated credential lifecycle"}, 201), &lc)
	lifecycleBase := fmt.Sprintf("/api/v1/lifecycles/%d", lc.ID)
	sharedID := rotationAPIVariable(
		t,
		f,
		lifecycleBase,
		"PRIVATE_SHARED",
		"original-shared",
	)
	revision := int64(0)
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		f.token = owner
		token := mintProjectToken(t, f, role, f.project.ID)
		f.token = token.ID
		f.api(t, "GET", f.base()+"/credentials", nil, 200)
		want := 403
		if role == "maintainer" || role == "admin" {
			want = 200
		}
		f.api(
			t,
			"PUT",
			fmt.Sprintf("%s/credentials/variable/%d", f.base(), id),
			map[string]any{
				"value":    "authorized-replacement",
				"revision": revision,
			},
			want,
		)
		if want == 200 {
			revision++
		}
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/projects/%d/credentials", other.ID),
			nil,
			403,
		)
		f.api(t, "GET", lifecycleBase+"/credentials", nil, 403)
		f.api(
			t,
			"PUT",
			fmt.Sprintf(
				"%s/credentials/lifecycle/%d",
				lifecycleBase,
				sharedID,
			),
			map[string]any{"value": "forbidden-replacement"},
			403,
		)
	}
	f.token = owner
}

func TestCredentialRotationRetainedPackageAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "PUT", f.base()+"/package-repository", map[string]string{
		"url_template": f.upstream + "/{version}.zip",
		"auth_type":    "bearer",
		"credential":   "artifact-secret",
	}, 200)
	var source struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "GET", f.base()+"/package-repository", nil, 200),
		&source,
	)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":            "Verify retained package",
		"script_body":     `test -d "$ARTIFACT_PATH" || exit 1; printf 'package-verified\n'`,
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "retained-rotation")
	f.api(t, "DELETE", f.base()+"/package-repository", nil, 204)
	f.changeCredential("rotated-package-token")
	path := fmt.Sprintf("%s/credentials/package/%d", f.base(), source.ID)
	f.api(
		t,
		"PUT",
		path,
		map[string]any{"value": "rotated-package-token", "expires_at": 1},
		200,
	)
	var summary repository.CredentialStatusSummary
	decodeLifecycleTest(
		t,
		f.api(t, "GET", f.base()+"/credentials/status", nil, 200),
		&summary,
	)
	require.Equal(
		t,
		1,
		summary.Expired,
		"retained package credentials remain in the project warning",
	)
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	require.Equal(
		t,
		"null",
		strings.TrimSpace(
			string(f.api(t, "GET", f.base()+"/package-repository", nil, 200)),
		),
	)
}

func TestCredentialRotationHTMXDenialsAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	id := rotationAPIVariable(t, f, f.base(), "PRIVATE_TOKEN", "original")
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Restricted credentials"}, 201), &lifecycle)
	lifecycleBase := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycle.ID)
	sharedID := rotationAPIVariable(
		t,
		f,
		lifecycleBase,
		"PRIVATE_SHARED",
		"original",
	)
	before, err := f.h.repo.Queries.GetVariable(t.Context(), id)
	require.NoError(t, err)
	sharedParams := db.GetLifecycleVariableParams{
		ID: sharedID, LifecycleID: lifecycle.ID,
	}
	sharedBefore, err := f.h.repo.Queries.GetLifecycleVariable(
		t.Context(),
		sharedParams,
	)
	require.NoError(t, err)
	for _, test := range []struct{ name, projectRole, path string }{
		{"viewer ceiling", "viewer", fmt.Sprintf("/projects/%d/credentials/variable/%d", f.project.ID, id)},
		{"deployer ceiling", "deployer", fmt.Sprintf("/projects/%d/credentials/variable/%d", f.project.ID, id)},
		{"nonmember", "", fmt.Sprintf("/projects/%d/credentials/variable/%d", f.project.ID, id)},
		{"shared administration", "", fmt.Sprintf("/lifecycles/%d/credentials/lifecycle/%d", lifecycle.ID, sharedID)},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := seedAPIUser(
				t,
				f.h.repo,
				strings.ReplaceAll(
					test.name,
					" ",
					"-",
				)+"@rotation.test",
				"maintainer",
			)
			if test.projectRole != "" {
				tstAddMember(t, f.h, f.project.ID, user.ID, test.projectRole)
			}
			session, csrf, err := auth.NewSessionToken()
			require.NoError(t, err)
			_, err = f.h.repo.Queries.CreateSession(
				t.Context(),
				db.CreateSessionParams{
					ID: session, UserID: user.ID, CsrfToken: csrf,
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
				},
			)
			require.NoError(t, err)
			form := url.Values{"csrf_token": {csrf},
				"revision": {"0"}, "value": {"forbidden-replacement"}}
			ownerPath := strings.Split(test.path, "/credentials")[0] + "/credentials"
			for _, path := range []string{test.path, ownerPath, ownerPath + "/enroll", test.path + "/delete"} {
				request, err := http.NewRequestWithContext(t.Context(), "POST",
					f.baseURL+path, strings.NewReader(form.Encode()))
				require.NoError(t, err)
				request.AddCookie(&http.Cookie{Name: "session", Value: session})
				request.Header.Set(
					"Content-Type",
					"application/x-www-form-urlencoded",
				)
				request.Header.Set("HX-Request", "true")
				response, err := f.client.Do(request)
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, response.Body)
				require.NoError(t, response.Body.Close())
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, response.StatusCode)
				require.Contains(
					t,
					response.Header.Get("HX-Trigger"),
					"makeToast",
				)
			}
		})
	}
	after, err := f.h.repo.Queries.GetVariable(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, before, after)
	sharedAfter, err := f.h.repo.Queries.GetLifecycleVariable(
		t.Context(),
		sharedParams,
	)
	require.NoError(t, err)
	require.Equal(t, sharedBefore, sharedAfter)
	var successful int
	require.NoError(t, f.h.repo.DB.QueryRow(
		"SELECT COUNT(*) FROM audit_log WHERE action IN ('update_credential_metadata', 'rotate_credential', 'create_credential', 'enroll_credential', 'delete_credential')",
	).Scan(&successful))
	require.Equal(
		t,
		2,
		successful,
		"only the two fixture credential creations succeed",
	)
}
