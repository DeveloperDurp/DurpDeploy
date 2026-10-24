//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
)

func seedVariableViewer(t *testing.T, f *artifactE2E) int64 {
	t.Helper()
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Viewer inheritance"}, 201), &lifecycle)
	lc := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycle.ID)
	f.api(t, "POST", lc+"/stages", map[string]int64{
		"environment_id": f.environment.ID,
	}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	for _, variable := range []map[string]any{
		{"name": "REGION", "value": "shared-default"},
		{"name": "REGION", "value": "shared-env", "environment_id": f.environment.ID},
		{"name": "SHARED_ONLY", "value": "inherited"},
		{"name": "SECRET", "value": "viewer-secret-sentinel", "secret": true},
	} {
		f.api(t, "POST", lc+"/variables", variable, 201)
	}
	for _, variable := range []map[string]any{
		{"name": "REGION", "value": "local-default"},
		{"name": "LOCAL", "value": "local-value"},
		{"name": "PATH", "value": "configured-path"},
		{"name": "BASH_ENV", "value": "configured-bash-env"},
	} {
		f.api(t, "POST", f.base()+"/variables", variable, 201)
	}
	return lifecycle.ID
}

func readVariableCatalog(
	t *testing.T,
	f *artifactE2E,
	query string,
) repository.AvailableVariables {
	t.Helper()
	data := f.api(t, "GET", f.base()+"/variables/available"+query, nil, 200)
	if strings.Contains(string(data), "viewer-secret-sentinel") {
		t.Fatal("catalog exposed secret plaintext")
	}
	var catalog repository.AvailableVariables
	decodeLifecycleTest(t, data, &catalog)
	return catalog
}

func effectiveViewerVariable(
	t *testing.T,
	catalog repository.AvailableVariables,
	name string,
) repository.VariableDefinition {
	t.Helper()
	for _, variable := range catalog.Configured {
		if variable.Name == name && variable.Effective {
			return variable
		}
	}
	t.Fatalf("missing effective definition for %s", name)
	return repository.VariableDefinition{}
}

func checkViewerCatalogDefinitions(
	t *testing.T, f *artifactE2E, query string,
) {
	t.Helper()
	catalog := readVariableCatalog(t, f, query)
	region := effectiveViewerVariable(t, catalog, "REGION")
	if region.Source != "lifecycle" || region.Value == nil ||
		*region.Value != "shared-env" {
		t.Fatal(
			"environment lifecycle value must beat an unscoped project override",
		)
	}
	secret := effectiveViewerVariable(t, catalog, "SECRET")
	if !secret.Secret || secret.Value != nil {
		t.Fatal("secret metadata must be retained without a display value")
	}
	for _, variable := range catalog.Configured {
		if variable.Name == "REGION" && variable.EnvironmentID == nil &&
			(variable.Status != "overridden" || variable.OverriddenBy == "") {
			t.Fatal("unscoped override chain was not explained")
		}
	}
	for _, variable := range readVariableCatalog(t, f, "").Configured {
		if variable.Effective || variable.Status == "available" {
			t.Fatal("missing environment must not imply an effective value")
		}
	}
}

func checkViewerReferences(t *testing.T, f *artifactE2E, query string) {
	t.Helper()
	for language, reference := range map[string]string{
		"bash": "${REGION}", "powershell": "$env:REGION",
		"python3": `os.environ.get("REGION")`,
	} {
		c := readVariableCatalog(
			t,
			f,
			query+"&interpreter="+language+"&variable_names=REGION,MISSING",
		)
		if effectiveViewerVariable(t, c, "REGION").Reference != reference ||
			effectiveViewerVariable(t, c, "LOCAL").Status != "excluded" ||
			!strings.Contains(strings.Join(c.Warnings, " "), "MISSING") {
			t.Fatal(
				"draft interpreter, selection, or unavailable name was ignored",
			)
		}
		for _, runtime := range c.Predefined {
			if runtime.Status != "conditional" || runtime.Value != nil ||
				!strings.Contains(runtime.Reason, "bypass variable_names") {
				t.Fatal(
					"managed runtime paths must remain conditional and bypass the allowlist",
				)
			}
		}
	}
}

func TestStepVariablesAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	lifecycleID := seedVariableViewer(t, f)
	query := fmt.Sprintf("?environment_id=%d", f.environment.ID)
	checkViewerCatalogDefinitions(t, f, query)
	checkViewerReferences(t, f, query)
	checkViewerExecutionTargets(t, f, query)
	f.api(t, "POST", f.base()+"/variables", map[string]any{
		"name":           "REGION",
		"value":          "project-env",
		"environment_id": f.environment.ID,
	}, 201)
	region := effectiveViewerVariable(
		t,
		readVariableCatalog(t, f, query),
		"REGION",
	)
	if region.Source != "project" || region.Value == nil ||
		*region.Value != "project-env" {
		t.Fatal("same-scope project definition must override lifecycle")
	}
	step := map[string]any{
		"name":            "viewer-runtime-proof",
		"script_body":     `test "$REGION" = project-env; test "$SECRET" = viewer-secret-sentinel; test -z "${LOCAL+x}"; echo viewer-runtime-ok`,
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"variable_names":  []string{"REGION", "SECRET"},
	}
	var saved struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/steps", step, 201),
		&saved,
	)
	selected := readVariableCatalog(
		t,
		f,
		query+fmt.Sprintf("&step_id=%d", saved.ID),
	)
	if effectiveViewerVariable(t, selected, "LOCAL").Status != "excluded" {
		t.Fatal("saved step selection was ignored")
	}
	reset := readVariableCatalog(
		t,
		f,
		query+fmt.Sprintf("&step_id=%d&variable_names=", saved.ID),
	)
	if effectiveViewerVariable(t, reset, "LOCAL").Status != "available" {
		t.Fatal(
			"explicit empty draft selection did not override saved selection",
		)
	}
	release := verificationRelease(t, f, "viewer-runtime")
	var deployment db.Deployment
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 201),
		&deployment,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	checkViewerRetiredScopes(t, f, lifecycleID)
	checkViewerAccess(t, f, saved.ID)
	web := f.web(
		t,
		"GET",
		strings.TrimPrefix(f.base(), "/api/v1")+"/variables/available"+query,
		nil,
		200,
	)
	if strings.Contains(web, "viewer-secret-sentinel") ||
		!strings.Contains(web, "predefined") {
		t.Fatal("session metadata API did not preserve masking and groups")
	}
}

func checkViewerExecutionTargets(t *testing.T, f *artifactE2E, query string) {
	t.Helper()
	for _, target := range []struct{ query, pathStatus, bashStatus string }{
		{"", "unsupported", "available"},
		{"&execution_target=agent", "available", "available"},
		{"&execution_target=agent&agent_execution_mode=container", "unsupported", "unsupported"},
	} {
		c := readVariableCatalog(t, f, query+target.query)
		if effectiveViewerVariable(t, c, "PATH").Status != target.pathStatus ||
			effectiveViewerVariable(
				t,
				c,
				"BASH_ENV",
			).Status != target.bashStatus {
			t.Fatal("execution target reserved-name rules differ")
		}
		if target.query != "" && c.Predefined[2].Status != "unsupported" {
			t.Fatal(
				"remote agent must not advertise approved artifact injection",
			)
		}
	}
}

func checkViewerRetiredScopes(t *testing.T, f *artifactE2E, lifecycleID int64) {
	t.Helper()
	other := seedEnv(t, f.h.repo)
	lc := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycleID)
	var stage struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", lc+"/stages", map[string]int64{
		"environment_id": other.ID,
	}, 201), &stage)
	f.api(t, "POST", lc+"/variables", map[string]any{
		"name": "RETIRED", "value": "retired", "environment_id": other.ID,
	}, 201)
	c := readVariableCatalog(t, f, fmt.Sprintf("?environment_id=%d", other.ID))
	if effectiveViewerVariable(t, c, "RETIRED").Status != "available" {
		t.Fatal("active lifecycle scope missing")
	}
	f.api(t, "POST", fmt.Sprintf("%s/stages/%d/delete", lc, stage.ID), nil, 204)
	for _, v := range readVariableCatalog(t, f, "").Configured {
		if v.Name == "RETIRED" {
			t.Fatal("retired lifecycle scope was inherited")
		}
	}
	f.api(
		t,
		"GET",
		f.base()+fmt.Sprintf(
			"/variables/available?environment_id=%d",
			other.ID,
		),
		nil,
		422,
	)
}

func checkViewerAccess(t *testing.T, f *artifactE2E, stepID int64) {
	t.Helper()
	base := f.base() + "/variables/available"
	for _, query := range []string{
		"?environment_id=-1", "?environment_id=invalid", "?environment_id=1&environment_id=2",
		"?step_id=0", "?interpreter=sh", "?execution_target=host",
		"?agent_execution_mode=container", "?variable_names=BAD-NAME",
	} {
		f.api(t, "GET", base+query, nil, 400)
	}
	f.api(t, "GET", base+"?step_id=99999999", nil, 404)
	other := seedProject(t, f.h.repo)
	foreign := fmt.Sprintf("/api/v1/projects/%d/variables/available", other.ID)
	f.api(t, "GET", foreign+fmt.Sprintf("?step_id=%d", stepID), nil, 404)
	owner := f.token
	token := mintProjectToken(t, f, "viewer", f.project.ID)
	f.token = token.ID
	readVariableCatalog(t, f, "")
	f.api(t, "GET", foreign, nil, 403)
	f.token = owner
	user := seedAPIUser(t, f.h.repo, "viewer-catalog@example.test", "viewer")
	_, f.token = seedAPIToken(t, f.h.repo, user.ID)
	f.api(t, "GET", base, nil, 403)
	if err := f.h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: user.ID, Role: "viewer",
		},
	); err != nil {
		t.Fatal(err)
	}
	readVariableCatalog(t, f, "")
	f.token = ""
	f.api(t, "GET", base, nil, 401)
	f.token = owner
}
