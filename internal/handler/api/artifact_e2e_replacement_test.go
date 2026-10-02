//go:build e2e

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestArtifactSourceReplacementE2E(t *testing.T) {
	// Given: an immutable release pinned to the original authenticated source.
	f := newArtifactE2E(t)
	old, release := f.createArtifactRelease(
		t,
		`set -eu; test "$(cat "$ARTIFACT_PATH/app.txt")" = package`,
	)
	pinPath := fmt.Sprintf("%s/releases/%d/artifact", f.base(), release.ID)
	before := f.api(t, "GET", pinPath, nil, 200)
	// When: replacing source identity and credential through the singleton API.
	body := f.api(t, "PUT", f.base()+"/package-repository", map[string]string{
		"url_template": f.upstream + "/replacement/{version}.zip",
		"auth_type":    "bearer", "credential": "different-source-secret",
	}, 200)
	var current db.PackageRepository
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatal(err)
	}
	if current.ID == old.ID {
		t.Fatal("source replacement overwrote the historical source")
	}
	body = f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 201)
	var deployment db.Deployment
	if err := json.Unmarshal(body, &deployment); err != nil {
		t.Fatal(err)
	}
	// Then: old release still uses its retained source/credential, not the new one.
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	if after := f.api(
		t,
		"GET",
		pinPath,
		nil,
		200,
	); !bytes.Equal(
		before,
		after,
	) {
		t.Fatal("replacement changed immutable release pin")
	}
	if bytes.Contains(body, []byte("secret")) {
		t.Fatal("deployment response exposed a repository credential")
	}
	// When: the old upstream rotates its token, explicitly save that exact source.
	f.changeCredential("rotated-old-source-secret")
	body = f.api(t, "PUT", f.base()+"/package-repository", map[string]string{
		"url_template": f.upstream + "/{version}.zip", "auth_type": "bearer",
		"credential": "rotated-old-source-secret",
	}, 200)
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatal(err)
	}
	if current.ID != old.ID {
		t.Fatal("historical source was not reused for credential rotation")
	}
	body = f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 201)
	if err := json.Unmarshal(body, &deployment); err != nil {
		t.Fatal(err)
	}
	// Then: the unchanged old release pin deploys with the newly supplied token.
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	if after := f.api(
		t,
		"GET",
		pinPath,
		nil,
		200,
	); !bytes.Equal(
		before,
		after,
	) {
		t.Fatal("historical credential rotation changed the pin")
	}
}
