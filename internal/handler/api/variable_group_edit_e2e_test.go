//go:build e2e

package api_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/config"
)

type variableGroupFixture struct {
	*artifactE2E
	dev, prd, other, devEnvironment int64
}

func newVariableGroupFixture(t *testing.T) variableGroupFixture {
	t.Helper()
	f := newConfiguredBodyFixture(t, config.Defaults().Limits.HTTP)
	environment := func(name string) int64 {
		var row struct{ ID int64 }
		decodeStepLogTest(t, f.api(t, "POST", "/api/v1/environments",
			map[string]string{"name": name}, 201), &row)
		return row.ID
	}
	dev, prd := environment("dev"), environment("prd")
	create := func(name, value string, scope int64, secret bool) int64 {
		var row struct{ ID int64 }
		decodeStepLogTest(
			t,
			f.api(t, "POST", f.base()+"/variables", map[string]any{
				"name": name, "value": value, "environment_id": scope, "secret": secret,
			}, 201),
			&row,
		)
		return row.ID
	}
	return variableGroupFixture{
		artifactE2E: f, devEnvironment: dev,
		dev:   create("APP_CONFIG", "dev-before", dev, false),
		prd:   create("APP_CONFIG", "prd-secret-before", prd, true),
		other: create("OTHER", "other-before", prd, false),
	}
}

func (f variableGroupFixture) groupPath() string {
	return fmt.Sprintf("%s/variables/%d/group", f.base(), f.dev)
}

func variableGroupFragmentRequest(r *http.Request) {
	r.Header.Set("HX-Request", "true")
}

func (f variableGroupFixture) assertValues(t *testing.T, dev, prd string) {
	t.Helper()
	for id, want := range map[int64]string{
		f.dev: dev, f.prd: prd, f.other: "other-before",
	} {
		row, err := f.h.repo.GetVariable(t.Context(), id)
		if err != nil || row.Value.String != want {
			t.Fatalf("unexpected stored value for %d (read error %v)", id, err)
		}
	}
}

func TestVariableGroupEditAPIWebE2E(t *testing.T) {
	f := newVariableGroupFixture(t)
	apiPath := f.groupPath()
	webPath := strings.TrimPrefix(apiPath, "/api/v1")
	update := func(right int64, value string) map[string]any {
		return map[string]any{"overrides": []map[string]any{
			{"id": f.dev, "value": value}, {"id": right, "value": ""},
		}}
	}
	f.api(t, "PUT", apiPath, update(f.dev, "duplicate"), 400)
	f.api(t, "PUT", apiPath, update(f.other, "must-roll-back"), 409)
	var otherProject struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "Other project"}, 201), &otherProject)
	f.api(
		t,
		"PUT",
		fmt.Sprintf(
			"/api/v1/projects/%d/variables/%d/group",
			otherProject.ID,
			f.dev,
		),
		update(f.prd, "wrong-project"),
		404,
	)
	viewer := seedAPIUser(t, f.h.repo, "group-viewer@example.test", "viewer")
	_, token := seedAPIToken(t, f.h.repo, viewer.ID)
	client := *f.artifactE2E
	client.token = token
	client.api(t, "PUT", apiPath, update(f.prd, "wrong-role"), 403)
	f.assertValues(t, "dev-before", "prd-secret-before")
	f.api(t, "PUT", apiPath, update(f.prd, "dev-api"), 204)
	f.assertValues(t, "dev-api", "prd-secret-before")

	f.web(t, "GET", webPath, nil, 303)
	for _, layout := range []string{"desktop", "mobile"} {
		page := f.web(
			t,
			"GET",
			webPath+"?layout="+layout,
			nil,
			200,
			variableGroupFragmentRequest,
		)
		if strings.Contains(page, "<html") ||
			strings.Count(page, "data-variable-group-field=") != 2 ||
			strings.Count(page, "hx-swap-oob=") != 1 ||
			strings.Contains(page, "prd-secret-before") ||
			!strings.Contains(page, "dev-api") {
			t.Fatalf("%s group editor did not load both masked values", layout)
		}
	}
	cancel := f.web(
		t,
		"GET",
		webPath+"?cancel=1",
		nil,
		200,
		variableGroupFragmentRequest,
	)
	if strings.Contains(cancel, "data-variable-group-form") ||
		strings.Count(cancel, "hx-swap-oob=") != 2 {
		t.Fatal("cancel did not return both read-only layouts")
	}
	devID, prdID := fmt.Sprint(f.dev), fmt.Sprint(f.prd)
	form := url.Values{
		"override_id":       {devID, prdID},
		"revision_" + devID: {"1"},
		"revision_" + prdID: {"0"},
		"value_" + devID:    {"dev-web"},
		"value_" + prdID:    {"prd-secret-web"},
	}
	page := f.web(t, "POST", webPath, form, 200, variableGroupFragmentRequest)
	if strings.Count(page, "hx-swap-oob=") != 2 ||
		strings.Contains(page, "prd-secret-web") ||
		!strings.Contains(page, "dev-web") {
		t.Fatal("save did not return both updated, masked group layouts")
	}
	f.assertValues(t, "dev-web", "prd-secret-web")
	form.Set("value_"+devID, "stale")
	page = f.web(t, "POST", webPath, form, 409, variableGroupFragmentRequest)
	if !strings.Contains(page, "The variable group changed.") ||
		strings.Contains(page, "prd-secret-web") {
		t.Fatal("stale editor did not report the conflict safely")
	}
	f.assertValues(t, "dev-web", "prd-secret-web")

	assertVariableGroupInactiveScope(t, f)
}

func assertVariableGroupInactiveScope(t *testing.T, f variableGroupFixture) {
	t.Helper()
	var lifecycle struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Dev only"}, 201), &lifecycle)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
		map[string]int64{"environment_id": f.devEnvironment}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	f.api(t, "PUT", f.groupPath(), map[string]any{"overrides": []map[string]any{
		{"id": f.dev, "value": "dev-active"},
	}}, 204)
	page := f.web(
		t,
		"GET",
		strings.TrimPrefix(f.groupPath(), "/api/v1"),
		nil,
		200,
		variableGroupFragmentRequest,
	)
	if strings.Count(page, `name="override_id"`) != 1 ||
		!strings.Contains(page, "move this override to an active environment") {
		t.Fatal("retained inactive scope was editable")
	}
	f.assertValues(t, "dev-active", "prd-secret-web")
}
