//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestReleaseMissingPackageAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	base := f.base()
	webBase := strings.TrimPrefix(base, "/api/v1")
	f.api(t, "PUT", base+"/package-repository", map[string]any{
		"url_template": f.upstream + "/{version}.zip",
		"auth_type":    "bearer", "credential": "artifact-secret",
	}, 200)
	f.api(t, "POST", base+"/steps", map[string]any{
		"name": "without-package", "script_body": `test -z "${ARTIFACT_PATH+x}"; test "$SNAPSHOT" = saved`,
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	f.api(t, "POST", base+"/variables", map[string]string{
		"name": "SNAPSHOT", "value": "saved",
	}, 201)
	f.changePackage("missing")
	// A normal request explains the missing version and saves nothing.
	failure := f.api(t, "POST", base+"/releases",
		map[string]any{"version": "2.0.0"}, 422)
	if !strings.Contains(string(failure), "2.0.0") ||
		!strings.Contains(string(failure), "HTTP 404") {
		t.Fatalf("unexplained failure: %s", failure)
	}
	page := f.web(t, "POST", webBase+"/releases",
		url.Values{"version": {"2.0.0"}}, 422)
	if !strings.Contains(page, `value="2.0.0"`) ||
		!strings.Contains(page, "data-create-release-anyway") {
		t.Fatal("web failure lost version or override")
	}
	// Both public surfaces support the explicit override.
	f.web(t, "POST", webBase+"/releases", url.Values{
		"version": {"web-missing"}, "allow_missing_package": {"true"},
	}, 303)
	body := f.api(t, "POST", base+"/releases", map[string]any{
		"version": "2.0.0", "allow_missing_package": true,
	}, 201)
	var release db.Release
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	if release.PackageOmitted != 1 ||
		!strings.Contains(release.StepsJson, "without-package") {
		t.Fatalf("incomplete release snapshot: %s", body)
	}
	releasePath := fmt.Sprintf("%s/releases/%d", base, release.ID)
	detail := f.api(t, "GET", releasePath, nil, 200)
	if !strings.Contains(string(detail), `"package_omitted":1`) ||
		!strings.Contains(string(detail), "saved") {
		t.Fatalf("incomplete API detail: %s", detail)
	}
	page = f.web(t, "GET", strings.TrimPrefix(releasePath, "/api/v1"), nil, 200)
	if !strings.Contains(page, "data-package-omitted") ||
		!strings.Contains(page, "ARTIFACT_PATH") {
		t.Fatal("release omitted-package warning missing")
	}
	f.api(t, "POST", base+"/releases", map[string]any{
		"version": "2.0.0", "allow_missing_package": true,
	}, 409)
	f.api(t, "POST", releasePath+"/refresh", nil, 422)
	// Publishing later does not mutate the release or add a deployment pin.
	f.changePackage("package")
	if pin := f.api(t, "GET", releasePath+"/artifact", nil, 200); strings.TrimSpace(
		string(pin),
	) != "null" {
		t.Fatalf("unexpected package pin: %s", pin)
	}
	body = f.api(t, "POST", base+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 201)
	var deployment db.Deployment
	if err := json.Unmarshal(body, &deployment); err != nil {
		t.Fatal(err)
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	// An explicit refresh pins the now-existing package and clears the warning.
	body = f.api(t, "POST", releasePath+"/refresh", nil, 200)
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	if release.PackageOmitted != 0 {
		t.Fatal("refresh did not clear package omission")
	}
	if pin := f.api(t, "GET", releasePath+"/artifact", nil, 200); !strings.Contains(
		string(pin),
		`"sha256"`,
	) {
		t.Fatal("refresh did not pin package")
	}
	// An override still rejects malformed ZIPs and authentication failures.
	f.changePackage("invalid")
	f.api(t, "POST", base+"/releases", map[string]any{
		"version": "invalid", "allow_missing_package": true,
	}, 422)
	f.changeCredential("rotated-fixture-secret")
	f.api(t, "POST", base+"/releases", map[string]any{
		"version": "unauthorized", "allow_missing_package": true,
	}, 502)
}
