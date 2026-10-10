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

func TestReleaseDeferredPackageAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	base := f.base()
	webBase := strings.TrimPrefix(base, "/api/v1")
	f.api(t, "PUT", base+"/package-repository", map[string]any{
		"url_template": f.upstream + "/{version}.zip",
		"auth_type":    "bearer", "credential": "artifact-secret",
	}, 200)
	f.api(t, "POST", base+"/steps", map[string]any{
		"name":            "with-package",
		"script_body":     `printf step-executed; test -f "$ARTIFACT_PATH/app.txt"; test "$SNAPSHOT" = saved`,
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	f.api(t, "POST", base+"/variables", map[string]string{
		"name": "SNAPSHOT", "value": "saved",
	}, 201)
	f.changePackage("missing")
	f.web(t, "POST", webBase+"/releases",
		url.Values{"version": {"web-deferred"}}, 303)
	body := f.api(t, "POST", base+"/releases",
		map[string]string{"version": "2.0.0"}, 201)
	var release db.Release
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(release.StepsJson, "with-package") {
		t.Fatalf("attachment or steps omitted: %s", body)
	}
	releasePath := fmt.Sprintf("%s/releases/%d", base, release.ID)
	var pin struct {
		Pending bool   `json:"pending"`
		SHA256  string `json:"sha256"`
		Size    int64  `json:"size"`
	}
	readPin := func() {
		t.Helper()
		if err := json.Unmarshal(
			f.api(t, "GET", releasePath+"/artifact", nil, 200),
			&pin,
		); err != nil {
			t.Fatal(err)
		}
	}
	readPin()
	if !pin.Pending || pin.SHA256 != "" || pin.Size != 0 {
		t.Fatalf("new package unexpectedly pinned: %+v", pin)
	}
	page := f.web(
		t,
		"GET",
		strings.TrimPrefix(releasePath, "/api/v1"),
		nil,
		200,
	)
	if !strings.Contains(page, "data-package-pending") ||
		strings.Contains(page, "data-package-omitted") {
		t.Fatal("web pending attachment state missing")
	}
	deploy := func(want events.Type) int64 {
		t.Helper()
		body := f.api(t, "POST", base+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 201)
		var deployment db.Deployment
		if err := json.Unmarshal(body, &deployment); err != nil {
			t.Fatal(err)
		}
		f.completion(t, deployment.ID, want)
		logs := string(
			f.api(
				t,
				"GET",
				fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
				nil,
				200,
			),
		)
		if strings.Contains(
			logs,
			"step-executed",
		) != (want == events.DeploymentSucceeded) {
			t.Fatalf(
				"deployment %d steps ran before a valid pull: %s",
				deployment.ID,
				logs,
			)
		}
		if want == events.DeploymentFailed &&
			!strings.Contains(logs, "Artifact staging failed:") {
			t.Fatalf("pull failure explanation missing: %s", logs)
		}
		return deployment.ID
	}
	deploy(events.DeploymentFailed)
	f.changePackage("invalid")
	deploy(events.DeploymentFailed)
	f.changePackage("package")
	f.changeCredential("rotated-secret")
	deploy(events.DeploymentFailed)
	readPin()
	if !pin.Pending {
		t.Fatal("failed pulls established a checksum")
	}
	f.changeCredential("artifact-secret")
	originalDeployment := deploy(events.DeploymentSucceeded)
	readPin()
	if pin.Pending || pin.SHA256 == "" || pin.Size == 0 {
		t.Fatal("first valid pull did not persist its checksum")
	}
	originalHash := pin.SHA256
	f.changePackage("changed-package")
	deploy(events.DeploymentFailed)
	// Refresh replaces the release checksum while existing deployment pins stay fixed.
	f.web(
		t,
		"POST",
		strings.TrimPrefix(releasePath, "/api/v1")+"/refresh",
		nil,
		303,
	)
	readPin()
	if pin.Pending || pin.SHA256 == originalHash {
		t.Fatal("refresh did not replace the release checksum")
	}
	deploy(events.DeploymentSucceeded)
	body = f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/redeploy", originalDeployment),
		nil,
		201,
	)
	var retry db.Deployment
	if err := json.Unmarshal(body, &retry); err != nil {
		t.Fatal(err)
	}
	f.completion(t, retry.ID, events.DeploymentFailed)
	// DNS failure permits creation, but fails during deployment pull.
	f.api(t, "PUT", base+"/package-repository", map[string]string{
		"url_template": "https://asdf/{version}", "auth_type": "noauth",
	}, 200)
	body = f.api(
		t,
		"POST",
		base+"/releases",
		map[string]string{"version": "unreachable"},
		201,
	)
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	deploy(events.DeploymentFailed)
}
