//go:build e2e && agentstaging

package agentserver_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type environmentPreflightScenario struct {
	name, legacyName, legacyValue string
	selected, ignoreLegacy        bool
	want                          string
	local                         bool
}

func TestDeploymentEnvironmentPreflightAPIE2E(t *testing.T) {
	s := newStagingE2E(t)
	for _, scenario := range []environmentPreflightScenario{
		{"nul-name", "BAD\x00NAME", "private-legacy", false, false, "failed", false},
		{"equals-name", "BAD=NAME", "private-legacy", false, false, "failed", false},
		{"space-name", "bad name", "private-legacy", false, false, "failed", false},
		{"digit-name", "2NAME", "private-legacy", false, false, "failed", false},
		{"nul-value", "LEGACY", "private\x00legacy", false, false, "failed", false},
		{"selected-nul-value", "LEGACY", "private\x00legacy", true, false, "failed", false},
		{"valid-overrides", "LEGACY", "", false, false, "succeeded", false},
		{"selected-ignores-invalid", "BAD\x00NAME", "private\x00legacy", true, true, "succeeded", false},
		{"local-blank-name", " ", "private-legacy", false, false, "failed", true},
		{"local-selected-name", "private=NAME", "private-legacy", true, false, "failed", true},
		{"local-nul-value", "LEGACY", "private\x00legacy", false, false, "failed", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base, environment := s.packageProject(
				t,
				"environment-"+scenario.name,
			)
			var other struct{ ID int64 }
			decodeEnvironmentPreflight(
				t,
				s.api(t, "POST", "/environments", map[string]string{
					"name": "unrelated-" + scenario.name,
				}, 201),
				&other,
			)
			for _, variable := range []map[string]any{
				{"name": "LEGACY", "value": "valid-before-snapshot", "secret": true},
				{"name": "Mixed_2", "value": "first\nsecond"},
				{"name": "_EMPTY", "value": ""},
				{"name": "TOKEN", "value": "secret-preflight-value", "secret": true},
				{"name": "OVERRIDE", "value": "valid-before-snapshot", "secret": true},
				{"name": "OVERRIDE", "value": "target", "environment_id": environment},
				{"name": "UNRELATED", "value": "valid-before-snapshot", "environment_id": other.ID},
			} {
				s.api(t, "POST", base+"/variables", variable, 201)
			}
			marker := filepath.Join(t.TempDir(), "script-started")
			step := map[string]any{
				"name": "environment preflight", "interpreter": "bash",
				"execution_target": "agent", "agent_execution_mode": "host",
				"agent_selectors": []string{"test-agent"},
				"script_body": fmt.Sprintf(`set -eu
touch %q
test "$Mixed_2" = 'first
second'
test "${_EMPTY-unset}" = ''
test "$OVERRIDE" = target
test "${UNRELATED-unset}" = unset
test "$TOKEN" = secret-preflight-value
printf 'secret=%%s\n' "$TOKEN"
echo environment-preflight-ok`, marker),
			}
			if scenario.local {
				step["execution_target"] = "local"
				step["container_image"] = "harbor.durp.info/dockerhub/library/bash:5.2"
				delete(step, "agent_execution_mode")
				delete(step, "agent_selectors")
			}
			if scenario.selected {
				names := []string{"Mixed_2", "_EMPTY", "TOKEN", "OVERRIDE"}
				if !scenario.ignoreLegacy {
					names = append(names, "LEGACY")
				}
				step["variable_names"] = names
			}
			s.api(t, "POST", base+"/steps", step, 201)
			var release, deployment struct{ ID int64 }
			decodeEnvironmentPreflight(
				t,
				s.api(t, "POST", base+"/releases", map[string]string{
					"version": "v1",
				}, 201),
				&release,
			)
			injectEnvironmentPreflightLegacy(
				t,
				s,
				scenario,
				release.ID,
				other.ID,
			)
			decodeEnvironmentPreflight(
				t,
				s.api(t, "POST", base+"/deployments", map[string]int64{
					"release_id": release.ID, "environment_id": environment,
				}, 201),
				&deployment,
			)
			s.wait(t, deployment.ID, scenario.want)
			logs := string(s.api(t, "GET", fmt.Sprintf(
				"/deployments/%d/logs.txt", deployment.ID,
			), nil, 200))
			assertEnvironmentPreflightOutcome(t, scenario.want, marker, logs)
			s.assertTransferPurged(t, scenario.name, deployment.ID)
		})
	}
}

func decodeEnvironmentPreflight(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func injectEnvironmentPreflightLegacy(
	t *testing.T,
	s *stagingE2E,
	scenario environmentPreflightScenario,
	releaseID, otherID int64,
) {
	t.Helper()
	// Simulate immutable legacy data after valid public API writes.
	// This also runs when MR !67 rejects malformed new variable writes.
	for _, patch := range []struct{ old, name, value string }{
		{"LEGACY", scenario.legacyName, scenario.legacyValue},
		{"OVERRIDE", "OVERRIDE", "overridden\x00private"},
		{"UNRELATED", "UNRELATED\x00NAME", "unrelated\x00private"},
	} {
		value, err := s.f.repo.EncryptValue(sql.NullString{
			String: patch.value, Valid: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.f.repo.DB.ExecContext(t.Context(), `UPDATE release_variables SET name=?,value=? WHERE release_id=? AND name=? AND (environment_id IS NULL OR environment_id=?)`, patch.name, value, releaseID, patch.old, otherID); err != nil {
			t.Fatal(err)
		}
	}
	if scenario.local && scenario.selected {
		stored, err := s.f.repo.Queries.GetRelease(
			t.Context(),
			releaseID,
		)
		if err != nil {
			t.Fatal(err)
		}
		var steps []map[string]any
		decodeEnvironmentPreflight(t, []byte(stored.StepsJson), &steps)
		steps[0]["variable_names"] = []string{
			"Mixed_2",
			"_EMPTY",
			"TOKEN",
			"OVERRIDE",
			scenario.legacyName,
		}
		data, err := json.Marshal(steps)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.f.repo.DB.ExecContext(t.Context(), `UPDATE releases SET steps_json=? WHERE id=?`, string(data), releaseID); err != nil {
			t.Fatal(err)
		}
	}
}

func assertEnvironmentPreflightOutcome(
	t *testing.T,
	want, marker, logs string,
) {
	t.Helper()
	if strings.Contains(logs, "private") ||
		strings.Contains(logs, "secret-preflight-value") {
		t.Fatalf("private environment value leaked: %s", logs)
	}
	_, markerErr := os.Stat(marker)
	if want == "failed" {
		if !strings.Contains(logs, "invalid execution environment") ||
			strings.Contains(logs, "Execution location:") ||
			!os.IsNotExist(markerErr) {
			t.Fatalf(
				"preflight did not fail before execution: marker=%v logs=%s",
				markerErr,
				logs,
			)
		}
	} else if markerErr != nil || !strings.Contains(logs, "environment-preflight-ok") || !strings.Contains(logs, "secret=[REDACTED]") {
		t.Fatalf("valid effective environment failed: marker=%v logs=%s", markerErr, logs)
	}
}
