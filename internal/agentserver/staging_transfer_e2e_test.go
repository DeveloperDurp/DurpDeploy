//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	agentexec "github.com/DeveloperDurp/durpdeploy-agent/executor"
)

func TestDeploymentStagingTransferAPIE2E(t *testing.T) {
	s := newStagingE2E(t)
	for _, scenario := range []string{"handoff", "fanout", "identical", "conflict", "failure", "cancel", "isolation", "assertion", "permissions", "renamed-directory", "escaped-directory", "renamed-root", "background-writer", "detached-writer", "cleanup-recovery", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			var project, environment struct{ ID int64 }
			decode := func(data []byte, target any) {
				t.Helper()
				if err := json.Unmarshal(data, target); err != nil {
					t.Fatal(err)
				}
			}
			decode(
				s.api(
					t,
					"POST",
					"/projects",
					map[string]string{"name": "staging-" + scenario},
					201,
				),
				&project,
			)
			decode(
				s.api(
					t,
					"POST",
					"/environments",
					map[string]string{"name": "staging-" + scenario},
					201,
				),
				&environment,
			)
			for _, id := range []string{"test-agent", "second-agent"} {
				s.api(
					t,
					"POST",
					"/admin/agents/"+id+"/environments",
					map[string]int64{"environment_id": environment.ID},
					204,
				)
			}
			base := fmt.Sprintf("/projects/%d", project.ID)
			add := func(target, mode, selector, script string) {
				t.Helper()
				step := map[string]any{
					"name":             "staging",
					"script_body":      "set -eu\n" + script,
					"execution_target": target,
					"interpreter":      "bash",
					"timeout_seconds":  120,
					"variable_names":   []string{"SELECTED"},
				}
				if target == "local" || mode == "container" {
					step["container_image"] = "docker.io/library/bash:5.2"
				}
				if target == "agent" {
					step["agent_execution_mode"] = mode
				}
				if selector != "" {
					step["agent_selectors"] = []string{selector}
				}
				s.api(t, "POST", base+"/steps", step, 201)
			}
			s.api(
				t,
				"POST",
				base+"/variables",
				map[string]string{"name": "SELECTED", "value": "selected"},
				201,
			)
			s.api(
				t,
				"POST",
				base+"/variables",
				map[string]string{"name": "UNRELATED", "value": "hidden"},
				201,
			)
			if scenario == "capacity" {
				add(
					"local",
					"",
					"",
					`trap 'stat -f -c "bytes=%S*%b free=%f inodes=%c free_inodes=%d" "$DURPDEPLOY_STAGE_DIR"; find "$DURPDEPLOY_STAGE_DIR" -maxdepth 1 | wc -l' EXIT; for ((n=1; n<20000; n++)); do printf x > "$DURPDEPLOY_STAGE_DIR/s$n"; done; head -c 536850913 /dev/zero > "$DURPDEPLOY_STAGE_DIR/large"`,
				)
				add(
					"agent",
					"container",
					"second-agent",
					`test "$(stat -c %s "$DURPDEPLOY_STAGE_DIR/large")" = 536850913; test -f "$DURPDEPLOY_STAGE_DIR/s19999"; echo transfer-ok`,
				)
			} else if scenario == "isolation" {
				add(
					"agent",
					"host",
					"test-agent",
					`test ! -e "$DURPDEPLOY_STAGE_DIR/seed"; test ! -e "$DURPDEPLOY_STAGE_DIR/from-agent"; echo isolated`,
				)
			} else {
				add(
					"local",
					"",
					"",
					`printf seed > "$DURPDEPLOY_STAGE_DIR/seed"; printf delete > "$DURPDEPLOY_STAGE_DIR/delete-me"`,
				)
				if scenario == "handoff" {
					add(
						"agent",
						"host",
						"test-agent",
						`test "$SELECTED" = selected; test "${UNRELATED-unset}" = unset; test "$(cat "$DURPDEPLOY_STAGE_DIR/seed")" = seed; printf agent > "$DURPDEPLOY_STAGE_DIR/from-agent"; rm "$DURPDEPLOY_STAGE_DIR/delete-me"`,
					)
					add(
						"agent",
						"container",
						"second-agent",
						`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-agent")" = agent; test ! -e "$DURPDEPLOY_STAGE_DIR/delete-me"; printf container > "$DURPDEPLOY_STAGE_DIR/from-container"`,
					)
				} else {
					script := `test "$(cat "$DURPDEPLOY_STAGE_DIR/seed")" = seed; printf "$LANG" > "$DURPDEPLOY_STAGE_DIR/$LANG"`
					switch scenario {
					case "identical":
						script = `printf identical > "$DURPDEPLOY_STAGE_DIR/shared"`
					case "conflict":
						script = `printf "$LANG" > "$DURPDEPLOY_STAGE_DIR/shared"`
					case "failure":
						script = `printf partial > "$DURPDEPLOY_STAGE_DIR/partial"; exit 7`
					case "assertion":
						script = `test "$(cat "$DURPDEPLOY_STAGE_DIR/seed")" = wrong; echo assertion-missed`
					case "permissions":
						script = `mkdir -p "$DURPDEPLOY_STAGE_DIR/locked/nested"; printf secret > "$DURPDEPLOY_STAGE_DIR/locked/nested/data"; chmod 000 "$DURPDEPLOY_STAGE_DIR/locked/nested" "$DURPDEPLOY_STAGE_DIR/locked"; exit 7`
					case "renamed-directory":
						script = `mv "$DURPDEPLOY_STAGE_DIR" "$DURPDEPLOY_STAGE_DIR-renamed"; mkdir "$DURPDEPLOY_STAGE_DIR"; printf replacement > "$DURPDEPLOY_STAGE_DIR/seed"`
					case "escaped-directory":
						script = `mv "$DURPDEPLOY_STAGE_DIR" "$(dirname "$(dirname "$DURPDEPLOY_STAGE_DIR")")/retained"`
					case "renamed-root":
						script = `root=$(dirname "$(dirname "$DURPDEPLOY_STAGE_DIR")"); mv "$root" "$(dirname "$root")/retained"`
					case "background-writer":
						script = `(sleep 2; mkdir -p "$DURPDEPLOY_STAGE_DIR"; printf retained > "$DURPDEPLOY_STAGE_DIR/retained") >/dev/null 2>&1 &`
					case "detached-writer":
						script = `setsid bash -c 'echo $$ > "$DURPDEPLOY_STAGE_DIR/group"; (echo "$BASHPID" > "$DURPDEPLOY_STAGE_DIR/child"; sleep 30) &' >/dev/null 2>&1 & while test ! -s "$DURPDEPLOY_STAGE_DIR/group" || test ! -s "$DURPDEPLOY_STAGE_DIR/child"; do sleep 0.01; done; echo "detached-group=$(cat "$DURPDEPLOY_STAGE_DIR/group")"; echo "detached-child=$(cat "$DURPDEPLOY_STAGE_DIR/child")"; rm "$DURPDEPLOY_STAGE_DIR/group" "$DURPDEPLOY_STAGE_DIR/child"`
					case "cleanup-recovery":
						script = `printf retained > "$DURPDEPLOY_STAGE_DIR/retained"; chmod 000 "$(dirname "$(dirname "$DURPDEPLOY_STAGE_DIR")")"; exit 7`
					case "cancel":
						script = `printf partial > "$DURPDEPLOY_STAGE_DIR/partial"; sleep 90`
					}
					add("agent", "host", "", script)
				}
				final := `test "$(cat "$DURPDEPLOY_STAGE_DIR/seed")" = seed; echo transfer-ok`
				if scenario == "handoff" {
					final = `test "$(cat "$DURPDEPLOY_STAGE_DIR/from-container")" = container; test ! -e "$DURPDEPLOY_STAGE_DIR/delete-me"; echo transfer-ok`
				}
				if scenario == "fanout" {
					final = `test -f "$DURPDEPLOY_STAGE_DIR/test-agent"; test -f "$DURPDEPLOY_STAGE_DIR/second-agent"; echo transfer-ok`
				}
				if scenario == "identical" {
					final = `test "$(cat "$DURPDEPLOY_STAGE_DIR/shared")" = identical; echo transfer-ok`
				}
				add("local", "", "", final)
			}
			var release, deployment struct{ ID int64 }
			decode(
				s.api(
					t,
					"POST",
					base+"/releases",
					map[string]string{"version": "v1"},
					201,
				),
				&release,
			)
			decode(
				s.api(
					t,
					"POST",
					base+"/deployments",
					map[string]int64{
						"release_id":     release.ID,
						"environment_id": environment.ID,
					},
					201,
				),
				&deployment,
			)
			want := "succeeded"
			if scenario == "conflict" || scenario == "failure" ||
				scenario == "assertion" ||
				scenario == "permissions" || scenario == "renamed-directory" ||
				scenario == "escaped-directory" || scenario == "renamed-root" ||
				scenario == "cleanup-recovery" {
				want = "failed"
			}
			if scenario == "cancel" {
				deadline := time.Now().Add(time.Minute)
				for {
					var started int
					if err := s.f.repo.DB.QueryRow("SELECT COUNT(*) FROM remote_step_runs WHERE deployment_id=? AND state='started'", deployment.ID).
						Scan(&started); err != nil {
						t.Fatal(err)
					}
					if started == 2 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("agents did not start")
					}
					time.Sleep(100 * time.Millisecond)
				}
				s.api(
					t,
					"POST",
					fmt.Sprintf("/deployments/%d/cancel", deployment.ID),
					nil,
					200,
				)
				want = "cancelled"
			}
			s.wait(t, deployment.ID, want)
			if scenario == "background-writer" {
				time.Sleep(3 * time.Second)
			}
			s.assertRuntimeStagingPurged(t)
			logs := string(
				s.api(
					t,
					"GET",
					fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
					nil,
					200,
				),
			)
			if scenario == "detached-writer" {
				groups := regexp.MustCompile(`detached-group=([0-9]+)`).
					FindAllStringSubmatch(logs, -1)
				for _, group := range groups {
					pid, err := strconv.Atoi(group[1])
					if err != nil || pid <= 0 {
						t.Fatal("invalid detached group", group)
					}
					t.Cleanup(func() { agentexec.KillProcessGroup(pid) })
				}
				children := regexp.MustCompile(`detached-child=([0-9]+)`).
					FindAllStringSubmatch(logs, -1)
				if len(children) != 2 || len(groups) != 2 {
					t.Fatalf("missing detached child evidence: %s", logs)
				}
				for _, child := range children {
					_, err := os.Stat("/proc/" + child[1] + "/stat")
					if !os.IsNotExist(err) {
						t.Fatalf(
							"completed deployment retained detached child: %s %v",
							child[1],
							err,
						)
					}
				}
			}
			if scenario == "assertion" &&
				strings.Contains(logs, "assertion-missed") {
				t.Fatal("failed assertion was ignored")
			}
			if scenario == "conflict" &&
				!strings.Contains(logs, "conflicting staging changes") {
				t.Fatalf("conflict missing in logs: %s", logs)
			}
			if want == "succeeded" && scenario != "isolation" &&
				!strings.Contains(logs, "transfer-ok") {
				t.Fatalf("final step missing: %s", logs)
			}
			if data := string(
				s.api(
					t,
					"GET",
					fmt.Sprintf("/deployments/%d/staging", deployment.ID),
					nil,
					200,
				),
			); strings.TrimSpace(
				data,
			) != "[]" {
				t.Fatalf("staging retained: %s", data)
			}
			movedRoots := 0
			for _, directory := range s.directories {
				entries, err := os.ReadDir(filepath.Join(directory, "staging"))
				if err != nil || len(entries) != 0 {
					t.Fatalf("staging directory retained: %v %v", entries, err)
				}
				if scenario == "renamed-root" {
					entries, err := os.ReadDir(
						filepath.Join(directory, "retained"),
					)
					if os.IsNotExist(err) {
						continue
					}
					if err != nil || len(entries) != 0 {
						t.Fatalf(
							"renamed staging root retained files: %v %v",
							entries,
							err,
						)
					}
					movedRoots++
				}
			}
			if scenario == "renamed-root" && movedRoots == 0 {
				t.Fatal("agents did not rename the staging root")
			}
		})
	}
}
