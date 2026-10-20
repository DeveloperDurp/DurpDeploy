//go:build e2e

package api_test

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestReleaseDuplicateRecoveryAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	version := `edge-<b>&"v1`
	release := verificationRelease(t, f, version)
	path := f.base() + "/releases"
	original := f.api(
		t,
		"GET",
		fmt.Sprintf("%s/%d", path, release.ID),
		nil,
		200,
	)
	body := f.api(t, "POST", path, map[string]string{"version": version}, 409)
	if !strings.Contains(string(body), "different version") {
		t.Fatal("duplicate API response has no recovery instruction")
	}
	webPath := strings.TrimPrefix(path, "/api/v1")
	page := f.web(t, "POST", webPath, url.Values{"version": {version}}, 422)
	if !strings.Contains(page, "different version") ||
		!strings.Contains(page, `value="`+html.EscapeString(version)+`"`) {
		t.Fatal("duplicate web rejection discarded the version or recovery")
	}
	checkDuplicateReleaseCount(t, f, 1)
	if got := f.api(t, "GET", fmt.Sprintf("%s/%d", path, release.ID), nil, 200); string(
		got,
	) != string(
		original,
	) {
		t.Fatal("duplicate rejection changed the existing release")
	}
	f.api(t, "POST", path, map[string]string{"version": "api-recovered"}, 201)
	f.web(t, "POST", webPath, url.Values{"version": {"web-recovered"}}, 303)
	checkDuplicateReleaseCount(t, f, 3)
}

func checkDuplicateReleaseCount(t *testing.T, f *artifactE2E, want int) {
	t.Helper()
	var releases struct{ Items []db.Release }
	decodeStepLogTest(
		t,
		f.api(t, "GET", f.base()+"/releases", nil, 200),
		&releases,
	)
	if len(releases.Items) != want {
		t.Fatalf("release count=%d, want %d", len(releases.Items), want)
	}
}
