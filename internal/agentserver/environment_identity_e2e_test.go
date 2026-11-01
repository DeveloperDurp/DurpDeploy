//go:build e2e && agentstaging

package agentserver_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentEnvironmentIdentityAPIE2E(t *testing.T) {
	s := newStagingE2E(t)
	base, dev := s.packageProject(t, "runtime-identity")
	s.api(
		t,
		"PUT",
		fmt.Sprintf("/environments/%d", dev),
		map[string]string{"name": "dev"},
		200,
	)
	var prd, special struct{ ID int64 }
	decodeEnvironmentPreflight(
		t,
		s.api(
			t,
			"POST",
			"/environments",
			map[string]string{"name": "prd"},
			201,
		),
		&prd,
	)
	const literal = `qa $(printf injected); 'quoted' & literal`
	decodeEnvironmentPreflight(
		t,
		s.api(
			t,
			"POST",
			"/environments",
			map[string]string{"name": literal},
			201,
		),
		&special,
	)
	for _, env := range []int64{dev, prd.ID, special.ID} {
		s.api(
			t,
			"POST",
			"/admin/agents/test-agent/environments",
			map[string]int64{"environment_id": env},
			204,
		)
	}
	s.api(
		t,
		"POST",
		base+"/variables",
		map[string]string{"name": "SELECTED", "value": "kept"},
		201,
	)
	s.api(
		t,
		"POST",
		base+"/variables",
		map[string]string{"name": "EXCLUDED", "value": "private"},
		201,
	)
	// Homebrew PowerShell fixtures do not include ICU in their base environment.
	s.api(
		t,
		"POST",
		base+"/variables",
		map[string]string{
			"name":  "DOTNET_SYSTEM_GLOBALIZATION_INVARIANT",
			"value": "1",
		},
		201,
	)
	for _, mode := range []string{"local", "host", "container"} {
		for _, language := range []string{"bash", "python3", "pwsh"} {
			s.api(t, "POST", base+"/steps", identityStep(mode, language), 201)
		}
	}
	var release struct{ ID int64 }
	decodeEnvironmentPreflight(
		t,
		s.api(
			t,
			"POST",
			base+"/releases",
			map[string]string{"version": "identity-v1"},
			201,
		),
		&release,
	)
	// Existing immutable snapshots may predate reservation of these names.
	for _, name := range []string{"DURPDEPLOY_ENVIRONMENT_NAME", "DURPDEPLOY_ENVIRONMENT_ID"} {
		value, err := s.f.repo.EncryptValue(
			sql.NullString{String: "legacy-wrong", Valid: true},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.f.repo.Queries.CreateReleaseVariable(
			t.Context(),
			db.CreateReleaseVariableParams{
				ReleaseID: release.ID, Name: name, Value: value, Secret: 1,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []struct {
		id   int64
		name string
	}{
		{dev, "dev"}, {prd.ID, "prd"}, {special.ID, literal}, {dev, "renamed-after-acceptance"},
	} {
		var deployment struct{ ID int64 }
		decodeEnvironmentPreflight(
			t,
			s.api(t, "POST", base+"/deployments", map[string]int64{
				"release_id": release.ID, "environment_id": target.id,
			}, 201),
			&deployment,
		)
		if target.name == "dev" {
			// Queueing, step retries, and later remote claims must keep dev.
			s.api(
				t,
				"PUT",
				fmt.Sprintf("/environments/%d", dev),
				map[string]string{"name": "renamed-after-acceptance"},
				200,
			)
		}
		s.wait(t, deployment.ID, "succeeded")
		logs := string(
			s.api(
				t,
				"GET",
				fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
				nil,
				200,
			),
		)
		checkIdentityExecution(t, logs, target.name, target.id)
	}
}

func checkIdentityExecution(t *testing.T, logs, name string, id int64) {
	t.Helper()
	for _, mode := range []string{"local", "host", "container"} {
		for _, language := range []string{"bash", "python3", "pwsh"} {
			want := fmt.Sprintf("%s-%s=%s|%d", mode, language, name, id)
			count := 1
			if language == "bash" {
				count = 2
			}
			if strings.Count(logs, want) != count {
				t.Fatalf(
					"missing accepted identity across attempts (%s, want %d): %s",
					want,
					count,
					logs,
				)
			}
		}
	}
	if strings.Contains(logs, "legacy-wrong") {
		t.Fatal("legacy conflicting value reached execution")
	}
}

func identityStep(mode, language string) map[string]any {
	name := mode + "-" + language
	image := "harbor.durp.info/dockerhub/library/bash:5.2"
	script := fmt.Sprintf(`set -eu
test "$SELECTED" = kept
test "${EXCLUDED-unset}" = unset
printf '%s=%%s|%%s\n' "$DURPDEPLOY_ENVIRONMENT_NAME" "$DURPDEPLOY_ENVIRONMENT_ID"
if [ ! -e "$DURPDEPLOY_STAGE_DIR/%s" ]; then
 touch "$DURPDEPLOY_STAGE_DIR/%s"
 exit 1
fi`, name, name, name)
	if language == "python3" {
		image = "harbor.durp.info/dockerhub/library/python:3.12"
		script = fmt.Sprintf(`import os
assert os.environ['SELECTED'] == 'kept'
assert 'EXCLUDED' not in os.environ
print('%s=' + os.environ['DURPDEPLOY_ENVIRONMENT_NAME'] + '|' + os.environ['DURPDEPLOY_ENVIRONMENT_ID'])`, name)
	}
	if language == "pwsh" {
		image = "harbor.durp.info/microsoft/powershell:latest"
		script = fmt.Sprintf(`$ErrorActionPreference = 'Stop'
if ($env:SELECTED -ne 'kept' -or (Test-Path Env:EXCLUDED)) { throw 'selection changed' }
[Console]::WriteLine('%s=' + $env:DURPDEPLOY_ENVIRONMENT_NAME + '|' + $env:DURPDEPLOY_ENVIRONMENT_ID)`, name)
	}
	step := map[string]any{
		"name": name, "interpreter": language, "script_body": script,
		"variable_names": []string{"SELECTED"}, "container_image": image,
	}
	if language == "bash" {
		step["max_retries"] = 1
	}
	if language == "python3" {
		step["variable_names"] = []string{
			"SELECTED",
			"DURPDEPLOY_ENVIRONMENT_NAME",
			"DURPDEPLOY_ENVIRONMENT_ID",
		}
	}
	if language == "pwsh" && mode == "host" {
		step["variable_names"] = []string{
			"SELECTED",
			"DOTNET_SYSTEM_GLOBALIZATION_INVARIANT",
		}
	}
	if mode != "local" {
		step["execution_target"], step["agent_execution_mode"] = "agent", mode
		step["agent_selectors"] = []string{"test-agent"}
		if mode == "host" {
			delete(step, "container_image")
		}
	}
	return step
}
