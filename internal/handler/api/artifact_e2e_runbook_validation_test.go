//go:build e2e

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func (f *artifactE2E) verifyRunbookArtifactValidation(
	t *testing.T, runbookID, pinnedReleaseID int64,
) {
	t.Helper()
	// Given: a pinned runbook, unpinned historical releases, and another project.
	other := seedProject(t, f.h.repo)
	foreign := f.seedForeignPinnedRelease(t, other.ID)
	unpinned := seedRelease(t, f.h.repo, f.project.ID)
	steps := []map[string]string{{
		"name": "check", "script_body": "true", "interpreter": "bash",
		"container_image": "docker.io/library/bash:5.2",
	}}
	path := fmt.Sprintf("%s/runbooks/%d", f.base(), runbookID)
	for _, test := range []struct {
		name      string
		releaseID int64
		keep      bool
		create    bool
		status    int
	}{
		{"negative-pin", -1, false, true, 422},
		{"missing-pin", 9223372036854775807, false, true, 404},
		{"foreign-pin", foreign.ID, false, true, 422},
		{"unpinned-release", unpinned.ID, false, true, 422},
		{"missing-selection", 0, false, true, 422},
		{"keep-new-book", 0, true, true, 422},
		{"conflicting-selection", pinnedReleaseID, true, false, 422},
	} {
		t.Run("runbook-artifact-"+test.name, func(t *testing.T) {
			// When: a save requests an invalid or inaccessible artifact pin.
			method, endpoint := "PUT", path
			if test.create {
				method, endpoint = "POST", f.base()+"/runbooks"
			}
			before := f.api(t, "GET", endpoint, nil, 200)
			body := f.api(t, method, endpoint, map[string]any{
				"name": test.name, "steps": steps,
				"artifact_release_id": test.releaseID,
				"keep_artifact_pin":   test.keep,
			}, test.status)

			// Then: the public API returns a structured error without saving it.
			var response struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(
				body,
				&response,
			); err != nil ||
				response.Error == "" {
				t.Fatalf("invalid error response: %s (%v)", body, err)
			}
			if after := f.api(
				t,
				"GET",
				endpoint,
				nil,
				200,
			); !bytes.Equal(
				before,
				after,
			) {
				t.Fatal("rejected save changed runbooks or their versions")
			}
		})
	}
	otherPath := fmt.Sprintf("/api/v1/projects/%d/runbooks", other.ID)
	body := f.api(t, "POST", otherPath, map[string]any{
		"name": "no-pin", "steps": steps,
	}, 201)
	var saved struct {
		Runbook db.Runbook `json:"runbook"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	unpinnedPath := fmt.Sprintf("%s/%d", otherPath, saved.Runbook.ID)
	before := f.api(t, "GET", unpinnedPath, nil, 200)
	// When: an existing runbook with no copied pin requests preservation.
	f.api(t, "PUT", unpinnedPath,
		map[string]any{"steps": steps, "keep_artifact_pin": true}, 422)
	// Then: it rejects the request, rather than creating an artifact-free version.
	if after := f.api(
		t,
		"GET",
		unpinnedPath,
		nil,
		200,
	); !bytes.Equal(
		before,
		after,
	) {
		t.Fatal("rejected pin preservation changed the runbook")
	}
}

func (f *artifactE2E) seedForeignPinnedRelease(
	t *testing.T,
	projectID int64,
) db.Release {
	t.Helper()
	base := fmt.Sprintf("/api/v1/projects/%d", projectID)
	f.api(t, "PUT", base+"/package-repository", map[string]string{
		"url_template": f.upstream + "/{version}.zip",
		"auth_type":    "bearer",
		"credential":   "artifact-secret",
	}, 200)
	f.api(t, "POST", base+"/steps", map[string]string{
		"name": "check", "script_body": "true", "interpreter": "bash",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	body := f.api(
		t,
		"POST",
		base+"/releases",
		map[string]string{"version": "foreign"},
		201,
	)
	var release db.Release
	if err := json.Unmarshal(body, &release); err != nil {
		t.Fatal(err)
	}
	f.api(t, "DELETE", base+"/package-repository", nil, 204)
	return release
}
