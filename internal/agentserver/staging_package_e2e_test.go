//go:build e2e && agentstaging

package agentserver_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
)

func TestDeploymentStagingPackageAPIE2E(t *testing.T) {
	s := newStagingE2E(t)
	for _, scenario := range []string{"handoff", "agent-first", "identical", "conflict", "failure", "cancel", "changed-package", "slow-package"} {
		t.Run(scenario, func(t *testing.T) {
			base, environmentID := s.packageProject(t, scenario)
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			entry, err := writer.Create("package.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write([]byte("package")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int64
			upstream := httptest.NewTLSServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get(
						"Authorization",
					) != "Bearer upstream-secret" {
						w.WriteHeader(401)
						return
					}
					count := requests.Add(1)
					if count > 1 && scenario == "slow-package" {
						time.Sleep(4 * time.Second)
					}
					data := archive.Bytes()
					if count > 1 && scenario == "changed-package" {
						data = []byte("republished")
					}
					if _, err := w.Write(data); err != nil {
						t.Error(err)
					}
				}),
			)
			defer upstream.Close()
			workspace, err := artifact.OpenWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer workspace.Close()
			s.f.repo.ArtifactClient = &artifact.Client{
				HTTP:    upstream.Client(),
				TempDir: workspace.Directory,
			}
			s.api(t, "PUT", base+"/package-repository", map[string]string{
				"url_template": upstream.URL + "/{version}.zip",
				"auth_type":    "bearer", "credential": "upstream-secret",
			}, 200)
			add := func(target, mode, selector, script string) {
				t.Helper()
				step := map[string]any{
					"name":             "package",
					"execution_target": target,
					"interpreter":      "bash",
					"timeout_seconds":  120,
					"script_body":      "set -eu\ntest \"$(cat \"$ARTIFACT_PATH/package.txt\")\" = package\n" + script,
				}
				if target == "local" || mode == "container" {
					step["container_image"] = "docker.io/library/bash:5.2"
					step["script_body"] = step["script_body"].(string) + "\n! touch \"$ARTIFACT_PATH/write\"\nawk '$5 == \"/artifacts\" { if ($6 !~ /ro/ || $6 !~ /noexec/) exit 1; found=1 } END { exit !found }' /proc/self/mountinfo"
				}
				if target == "agent" {
					step["agent_execution_mode"] = mode
				}
				if selector != "" {
					step["agent_selectors"] = []string{selector}
				}
				if scenario == "slow-package" && target == "agent" {
					step["timeout_seconds"] = 2
				}
				s.api(t, "POST", base+"/steps", step, 201)
			}
			if scenario == "handoff" {
				add(
					"local",
					"",
					"",
					`test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-server"`,
				)
				add(
					"agent",
					"host",
					"test-agent",
					`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-server")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-host"`,
				)
				add(
					"agent",
					"container",
					"second-agent",
					`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-host")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-container"`,
				)
			} else if scenario == "agent-first" {
				add(
					"agent",
					"host",
					"test-agent",
					`test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-host"`,
				)
				add(
					"agent",
					"container",
					"second-agent",
					`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-host")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-container"`,
				)
			} else {
				script := `cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-container"`
				selector := ""
				switch scenario {
				case "conflict":
					script = `printf "$LANG" > "$DURPDEPLOY_STAGE_DIR/from-container"`
				case "failure":
					script += "; exit 7"
				case "cancel":
					script += "; sleep 90"
				case "slow-package", "changed-package":
					selector = "test-agent"
				}
				add("agent", "host", selector, script)
			}
			add(
				"local",
				"",
				"",
				`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-container")" = package; test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; echo package-transfer-ok`,
			)
			var release, deployment struct{ ID int64 }
			if err := json.Unmarshal(
				s.api(
					t,
					"POST",
					base+"/releases",
					map[string]string{"version": "v1"},
					201,
				),
				&release,
			); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(
				s.api(t, "POST", base+"/deployments", map[string]int64{
					"release_id": release.ID, "environment_id": environmentID,
				}, 201),
				&deployment,
			); err != nil {
				t.Fatal(err)
			}
			want := "succeeded"
			switch scenario {
			case "conflict", "failure", "changed-package":
				want = "failed"
			case "cancel":
				s.cancelRunningStaging(t, deployment.ID)
				want = "cancelled"
			}
			s.wait(t, deployment.ID, want)
			logs := string(
				s.api(
					t,
					"GET",
					fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
					nil,
					200,
				),
			)
			if want == "succeeded" &&
				!strings.Contains(logs, "package-transfer-ok") {
				t.Fatalf("missing final package read: %s", logs)
			}
			if strings.Contains(logs, "upstream-secret") {
				t.Fatal("package credential leaked")
			}
			s.assertTransferPurged(t, scenario, deployment.ID)
			s.assertRuntimeStagingPurged(t)
			files, err := os.ReadDir(workspace.Directory)
			if err != nil || len(files) != 1 || files[0].Name() != ".lock" {
				t.Fatalf("server package files retained: %v %v", files, err)
			}
		})
	}
}

func (s *stagingE2E) packageProject(
	t *testing.T,
	scenario string,
) (string, int64) {
	t.Helper()
	var project, environment struct{ ID int64 }
	for path, target := range map[string]*struct{ ID int64 }{
		"/projects": &project, "/environments": &environment,
	} {
		if err := json.Unmarshal(s.api(t, "POST", path, map[string]string{
			"name": "package-" + scenario,
		}, 201), target); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"test-agent", "second-agent"} {
		s.api(t, "POST", "/admin/agents/"+id+"/environments", map[string]int64{
			"environment_id": environment.ID,
		}, 204)
	}
	return fmt.Sprintf("/projects/%d", project.ID), environment.ID
}
