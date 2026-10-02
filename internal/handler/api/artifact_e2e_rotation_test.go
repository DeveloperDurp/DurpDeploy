//go:build e2e

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestArtifactCredentialRotationE2E(t *testing.T) {
	// Given: an immutable release pinned using the original bearer credential.
	f := newArtifactE2E(t)
	source, release := f.createArtifactRelease(
		t,
		`set -eu; test "$(cat "$ARTIFACT_PATH/app.txt")" = package; echo rotation-readable`,
	)
	pinPath := fmt.Sprintf("%s/releases/%d/artifact", f.base(), release.ID)
	before := f.api(t, "GET", pinPath, nil, 200)
	f.changeCredential("rotated-artifact-secret")

	// When: the public API rotates credentials and deploys that existing release.
	update := f.api(
		t,
		"PUT",
		fmt.Sprintf("%s/package-repositories/%d", f.base(), source.ID),
		map[string]string{
			"name": "packages", "url_template": f.upstream + "/{version}.zip",
			"auth_type": "bearer", "credential": "rotated-artifact-secret",
		},
		200,
	)
	body := f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 201)
	var deployment db.Deployment
	if err := json.Unmarshal(body, &deployment); err != nil {
		t.Fatal(err)
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)

	// Then: the new credential works without changing the pin or exposing secrets.
	after := f.api(t, "GET", pinPath, nil, 200)
	if !bytes.Equal(before, after) {
		t.Fatal("credential rotation changed the immutable pin")
	}
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(string(logs), "rotation-readable") {
		t.Fatal("pinned package was not read after rotation")
	}
	read := f.api(
		t,
		"GET",
		fmt.Sprintf("%s/package-repositories/%d", f.base(), source.ID),
		nil,
		200,
	)
	for _, response := range [][]byte{before, update, body, after, logs, read} {
		if bytes.Contains(response, []byte("artifact-secret")) {
			t.Fatal("repository credential exposed in API response or logs")
		}
	}
}

func (f *artifactE2E) createArtifactRelease(
	t *testing.T,
	script string,
) (db.PackageRepository, db.Release) {
	t.Helper()
	body := f.api(
		t,
		"POST",
		f.base()+"/package-repositories",
		map[string]string{
			"name": "packages", "url_template": f.upstream + "/{version}.zip",
			"auth_type": "bearer", "credential": "artifact-secret",
		},
		201,
	)
	var source db.PackageRepository
	if err := json.Unmarshal(body, &source); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"PUT",
		f.base()+"/artifact-repository",
		map[string]int64{"repository_id": source.ID},
		200,
	)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "read", "script_body": script, "interpreter": "bash",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	body = f.api(
		t,
		"POST",
		f.base()+"/releases",
		map[string]string{"version": "1.0"},
		201,
	)
	var release db.Release
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	return source, release
}
