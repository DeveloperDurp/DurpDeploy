//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

const releaseContentSecret = "release-content-secret-sentinel"

type releaseContentFixture struct {
	f       *artifactE2E
	path    string
	scripts []string
	names   []string
}

func newReleaseContent(t *testing.T) releaseContentFixture {
	t.Helper()
	f := newArtifactE2E(t)
	fixture := releaseContentFixture{
		f: f,
		scripts: []string{
			strings.Repeat("# script line\n", 8) +
				"printf '雪🙂 <tag> & </code><script>" +
				"window.releaseContentInjected=true</script>'\n" +
				strings.Repeat("x", 400),
			"printf 'second snapshot script'",
		},
		names: []string{
			"RELEASE_" + strings.Repeat("LONG_NAME_", 24),
			"SECRET_" + strings.Repeat("S", 248),
		},
	}
	var firstStepID, firstVariableID int64
	for index, script := range fixture.scripts {
		var created struct{ ID int64 }
		decodeStepLogTest(t, f.api(t, "POST", f.base()+"/steps", map[string]any{
			"name":        fmt.Sprintf("Step %d — full script", index+1),
			"script_body": script, "sort_order": index + 1,
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		}, 201), &created)
		if index == 0 {
			firstStepID = created.ID
		}
	}
	for index, name := range fixture.names {
		value := "visible & <value>"
		if index == 1 {
			value = releaseContentSecret
		}
		var created struct{ ID int64 }
		decodeStepLogTest(
			t,
			f.api(t, "POST", f.base()+"/variables", map[string]any{
				"name": name, "value": value,
				"secret": index == 1,
			}, 201),
			&created,
		)
		if index == 0 {
			firstVariableID = created.ID
		}
	}
	release := verificationRelease(t, f, "full-content-v1")
	fixture.path = fmt.Sprintf("%s/releases/%d", f.base(), release.ID)
	f.api(t, "PUT", fmt.Sprintf("%s/steps/%d", f.base(), firstStepID),
		map[string]any{
			"name": "Current project step", "script_body": "printf current-only",
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		}, 200)
	f.api(t, "PUT", fmt.Sprintf("%s/variables/%d", f.base(), firstVariableID),
		map[string]string{"name": "CURRENT_ONLY", "value": "current"}, 200)
	return fixture
}

func (fixture releaseContentFixture) viewer(t *testing.T) (string, string) {
	t.Helper()
	f := fixture.f
	user := seedAPIUser(t, f.h.repo, "content-viewer@example.test", "viewer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: user.ID, Role: "viewer",
		}); err != nil {
		t.Fatal(err)
	}
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	return token, memberRoleSession(t, f.h, user.ID)
}

func (fixture releaseContentFixture) checkAPI(t *testing.T) {
	t.Helper()
	var result struct {
		StepsJSON string `json:"steps_json"`
		Variables []struct {
			Name, Value string
			Secret      int64
		}
	}
	decodeStepLogTest(t, fixture.f.api(t, "GET", fixture.path, nil, 200),
		&result)
	var steps []struct {
		ScriptBody string `json:"script_body"`
	}
	if err := json.Unmarshal([]byte(result.StepsJSON), &steps); err != nil {
		t.Fatal(err)
	}
	if len(steps) != len(fixture.scripts) || len(result.Variables) != 2 {
		t.Fatal("release content is incomplete")
	}
	for index, step := range steps {
		if step.ScriptBody != fixture.scripts[index] {
			t.Fatal("release script was shortened or changed")
		}
	}
	values := make(map[string]string)
	for _, variable := range result.Variables {
		values[variable.Name] = variable.Value
		if variable.Secret != 0 && variable.Value != "" {
			t.Fatal("release secret was exposed")
		}
	}
	_, hasSecret := values[fixture.names[1]]
	if values[fixture.names[0]] != "visible & <value>" ||
		!hasSecret || values[fixture.names[1]] != "" {
		t.Fatal("release variable names or masking changed")
	}
}

func TestReleaseContentAPIWebE2E(t *testing.T) {
	fixture := newReleaseContent(t)
	f := fixture.f
	viewerToken, viewerSession := fixture.viewer(t)
	for _, identity := range []struct{ token, session string }{
		{f.token, f.session}, {viewerToken, viewerSession},
	} {
		f.token, f.session = identity.token, identity.session
		fixture.checkAPI(t)
		page := f.web(t, "GET", strings.TrimPrefix(fixture.path, "/api/v1"),
			nil, 200)
		for _, marker := range []string{
			"data-release-script", "data-release-variable-name",
			"View full script", "View full variable name",
		} {
			if !strings.Contains(page, marker) {
				t.Fatal("release has no full-content disclosure: " + marker)
			}
		}
		if !strings.Contains(page, html.EscapeString(fixture.scripts[0])) ||
			!strings.Contains(page, fixture.names[1]) ||
			strings.Contains(page, releaseContentSecret) ||
			strings.Contains(page, "<script>window.releaseContentInjected") {
			t.Fatal("full release content is missing or unescaped")
		}
	}
}
