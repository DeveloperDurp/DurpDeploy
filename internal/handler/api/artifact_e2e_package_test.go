//go:build e2e

package api_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/swagger"
)

func (f *artifactE2E) verifyPackageVersionCheck(t *testing.T) {
	t.Helper()
	// Given: a saved source is active immediately and the project has no release.
	before := f.api(t, "GET", f.base()+"/releases", nil, 200)
	path := f.base() + "/package-repository/test"
	// When: testing a version through the real API and web form.
	body := f.api(t, "POST", path, map[string]string{"version": "1.6.0"}, 200)
	var result struct {
		Exists bool `json:"exists"`
		artifact.Pin
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Exists || result.Version != "1.6.0" ||
		result.URL != f.upstream+"/1.6.0.zip" ||
		len(result.SHA256) != 64 ||
		result.Size <= 0 {
		t.Fatalf("incomplete package test result: %s", body)
	}
	page := f.web(
		t,
		"POST",
		strings.TrimPrefix(f.base(), "/api/v1")+"/package-repository/test",
		url.Values{"version": {"1.6.0"}},
		200,
	)
	if !strings.Contains(page, `data-package-test="success"`) ||
		strings.Contains(page, "artifact-secret") {
		t.Fatal("web package test failed or exposed credentials")
	}
	// Then: testing does not create a release or alter the configuration.
	if after := f.api(
		t,
		"GET",
		f.base()+"/releases",
		nil,
		200,
	); !bytes.Equal(
		before,
		after,
	) {
		t.Fatal("package test created a release")
	}
	for _, version := range []string{"", ".", "..", "../escape"} {
		f.api(t, "POST", path, map[string]string{"version": version}, 422)
	}
	for _, payload := range []string{"missing", "invalid"} {
		f.changePackage(payload)
		status := 422
		if payload == "missing" {
			status = 502
		}
		body := f.api(
			t,
			"POST",
			path,
			map[string]string{"version": "1.6.0"},
			status,
		)
		if bytes.Contains(body, []byte("artifact-secret")) {
			t.Fatal("upstream failure exposed credentials")
		}
	}
	f.changePackage("package")
	contract, err := swagger.ReadSpec()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]struct {
			Post struct {
				Responses map[string]json.RawMessage `json:"responses"`
			} `json:"post"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(contract, &spec); err != nil {
		t.Fatal(err)
	}
	responses := spec.Paths["/projects/{id}/package-repository/test"].Post.Responses
	for _, status := range []string{"200", "400", "401", "403", "404", "422", "500", "502"} {
		if _, ok := responses[status]; !ok {
			t.Fatalf("package test status %s missing from Swagger", status)
		}
	}
}
