//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

func stagingRuntime(t *testing.T, args ...string) string {
	t.Helper()
	kind := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	if endpoint := os.Getenv("DURPDEPLOY_CONTAINER_URL"); endpoint != "" {
		prefix := []string{"--host=" + endpoint}
		if kind == "podman" {
			prefix = []string{"--remote", "--url=" + endpoint}
		}
		args = append(prefix, args...)
	}
	output, err := exec.CommandContext(t.Context(), kind, args...).
		CombinedOutput()
	if err != nil {
		t.Fatalf("runtime %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestDeploymentStagingCleanupE2E(t *testing.T) {
	for _, terminal := range []string{"succeeded", "failed", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			// Given: a real API deployment creates a staging file.
			f := newArtifactE2E(t)
			script := fmt.Sprintf(`set -eu
stat -f -c '%%S %%b %%c' "$DURPDEPLOY_STAGE_DIR" | awk '{ if ($1 * $2 != %d || $3 != %d) exit 1 }'
printf staged > "$DURPDEPLOY_STAGE_DIR/file"
printf 'stage-ready\n'
`, artifact.MaxExtracted+int64(artifact.MaxFiles)*int64(os.Getpagesize()), artifact.MaxStagingNodes+1)
			switch terminal {
			case "failed":
				script += "exit 7\n"
			case "cancelled":
				script += "sleep 30\n"
			}
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": "stage", "script_body": script,
				"container_image": "docker.io/library/bash:5.2",
			}, 201)
			var release db.Release
			if err := json.Unmarshal(f.api(t, "POST", f.base()+"/releases",
				map[string]string{"version": "1"}, 201), &release); err != nil {
				t.Fatal(err)
			}
			var dep db.Deployment
			if err := json.Unmarshal(f.api(t, "POST", f.base()+"/deployments",
				map[string]int64{"release_id": release.ID, "environment_id": f.environment.ID}, 201), &dep); err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/api/v1/deployments/%d", dep.ID)
			// When: the deployment succeeds, fails, or is cancelled after writing.
			logs := awaitStagingTerminal(t, f, path, terminal)
			if !strings.Contains(string(logs), "stage-ready") {
				t.Fatal("deployment completed without writing the staged file")
			}
			// Then: terminal status is not visible until volume and keeper are gone.
			filter := "--filter=label=io.durpdeploy.namespace=" +
				os.Getenv(
					"DURPDEPLOY_CONTAINER_RUNTIME",
				) + ":" + os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")
			if volumes := stagingRuntime(t, "volume", "ls", "--quiet", filter); volumes != "" {
				t.Fatalf(
					"terminal deployment left staging volumes: %s",
					volumes,
				)
			}
			if containers := stagingRuntime(t, "ps", "--all", "--quiet", filter); containers != "" {
				t.Fatalf("terminal deployment left containers: %s", containers)
			}
		})
	}
}

func awaitStagingTerminal(
	t *testing.T,
	f *artifactE2E,
	path, terminal string,
) []byte {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	cancelled := false
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
		if err := json.Unmarshal(f.api(t, "GET", path+"/status", nil, 200), &state); err != nil {
			t.Fatal(err)
		}
		if state.Status == terminal {
			return logs
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
