//go:build e2e

package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/staging"
	"github.com/stretchr/testify/require"
)

func stagingRuntime(t *testing.T, args ...string) string {
	t.Helper()
	return stagingRuntimeBinary(
		t,
		os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME"),
		args...)
}

func stagingRuntimeBinary(t *testing.T, binary string, args ...string) string {
	t.Helper()
	kind := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	if endpoint := os.Getenv("DURPDEPLOY_CONTAINER_URL"); endpoint != "" {
		prefix := []string{"--host=" + endpoint}
		if kind == "podman" {
			prefix = []string{"--remote", "--url=" + endpoint}
		}
		args = append(prefix, args...)
	}
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(t.Context()),
		time.Minute,
	)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, args...).
		CombinedOutput()
	if err != nil {
		t.Fatalf("runtime %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func cleanupArtifactRuntime(t *testing.T, h *harness, binary ...string) {
	t.Helper()
	cleanupBinary := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	if len(binary) != 0 {
		cleanupBinary = binary[0]
	}
	t.Cleanup(func() {
		h.runner.KillAll()
		// Test-owned references may outlive an awaiting fixture.
		ids := stagingRuntimeBinary(t, cleanupBinary, "ps", "--all", "--quiet",
			"--filter=label=io.durpdeploy.namespace="+
				os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")+":"+
				os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")+":gate-images",
			"--filter=label=io.durpdeploy.gate-image")
		if ids != "" {
			stagingRuntimeBinary(
				t,
				cleanupBinary,
				append([]string{"rm", "--force", "--volumes"},
					strings.Fields(ids)...)...)
		}
	})
}

func TestDeploymentStagingCleanupE2E(t *testing.T) {
	t.Run("terminal_logs", testAwaitStagingTerminalReadsFinalLogs)
	for _, scenario := range []string{
		"succeeded", "failed", "cancelled", "cancelled_during_setup",
	} {
		t.Run(scenario, func(t *testing.T) {
			terminal := strings.Split(scenario, "_")[0]
			// Given: a real API deployment creates a staging file.
			f := newArtifactE2E(t)
			script := fmt.Sprintf(`set -eu
stat -f -c '%%S %%b %%c' "$DURPDEPLOY_STAGE_DIR" | awk '{ if ($1 * $2 != %d || $3 != %d) exit 1 }'
printf staged > "$DURPDEPLOY_STAGE_DIR/file"
printf 'stage-ready\n'
			`, staging.MaxBytes+int64(staging.MaxFiles)*int64(os.Getpagesize()), 2*(staging.MaxFiles+1))
			switch terminal {
			case "failed":
				script += "exit 7\n"
			case "cancelled":
				script += "sleep 30\n"
			}
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": "stage", "script_body": script,
				"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
			}, 201)
			var release db.Release
			decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
				map[string]string{"version": "1"}, 201), &release)
			var dep db.Deployment
			decodeStepLogTest(t, f.api(
				t,
				"POST",
				f.base()+"/deployments",
				map[string]int64{
					"release_id":     release.ID,
					"environment_id": f.environment.ID,
				},
				201,
			), &dep)
			path := fmt.Sprintf("/api/v1/deployments/%d", dep.ID)
			filter := "--filter=label=io.durpdeploy.namespace=" +
				os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") + ":" +
				os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")
			if scenario == "cancelled_during_setup" {
				cancelStagingSetup(t, f, path, filter)
			}
			// When: cancellation can interrupt setup or follow a staged write.
			logs := awaitStagingTerminal(t, f, path, scenario)
			if scenario != "cancelled_during_setup" &&
				!strings.Contains(string(logs), "stage-ready") {
				t.Fatal("deployment completed without writing the staged file")
			}
			// Then: terminal status is not visible until volume and keeper are gone.
			require.Empty(t, stagingRuntime(
				t,
				"volume",
				"ls",
				"--quiet",
				filter,
			), "terminal deployment left staging volumes")
			require.Empty(t, stagingRuntime(
				t,
				"ps",
				"--all",
				"--quiet",
				filter,
			), "terminal deployment left containers")
		})
	}
}

func cancelStagingSetup(t *testing.T, f *artifactE2E, path, filter string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for stagingRuntime(t, "volume", "ls", "--quiet", filter) == "" {
		if time.Now().After(deadline) {
			t.Fatal("staging setup did not start")
		}
		time.Sleep(time.Millisecond)
	}
	f.api(t, "POST", path+"/cancel", nil, 200)
}

func awaitStagingTerminal(
	t *testing.T,
	f *artifactE2E,
	path, terminal string,
) []byte {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	cancelled := terminal == "cancelled_during_setup"
	if cancelled {
		terminal = "cancelled"
	}
	for {
		logs := f.api(t, "GET", path+"/logs", nil, 200)
		if terminal == "cancelled" && !cancelled &&
			strings.Contains(string(logs), "stage-ready") {
			f.api(t, "POST", path+"/cancel", nil, 200)
			cancelled = true
		}
		var state struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(
			f.api(t, "GET", path+"/status", nil, 200),
			&state,
		); err != nil {
			t.Fatal(err)
		}
		if state.Status == terminal {
			return f.api(t, "GET", path+"/logs", nil, 200)
		}
		if state.Status == "cleanup_unconfirmed" || state.Status == "failed" ||
			time.Now().After(deadline) {
			t.Fatalf(
				"deployment=%s want=%s logs=%s",
				state.Status,
				terminal,
				logs,
			)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func testAwaitStagingTerminalReadsFinalLogs(t *testing.T) {
	const deploymentPath = "/api/v1/deployments/1"
	const finalLogs = `[{"line":"restore-execution-ok"}]`
	var terminal atomic.Bool
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := "[]"
			switch r.URL.Path {
			case deploymentPath + "/logs":
				if terminal.Load() {
					body = finalLogs
				}
			case deploymentPath + "/status":
				// Completion occurs after the caller's first log read.
				terminal.Store(true)
				body = `{"status":"succeeded"}`
			default:
				http.NotFound(w, r)
				return
			}
			if _, err := fmt.Fprint(w, body); err != nil {
				t.Error(err)
			}
		}),
	)
	t.Cleanup(server.Close)
	f := &artifactE2E{baseURL: server.URL, client: server.Client()}
	if logs := awaitStagingTerminal(t, f, deploymentPath, "succeeded"); string(
		logs,
	) != finalLogs {
		t.Fatalf("terminal logs=%s want=%s", logs, finalLogs)
	}
}
