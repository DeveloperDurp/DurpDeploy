//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/events"
)

func TestDeploymentStepResourcesE2E(t *testing.T) {
	for _, scenario := range []string{"ordinary", "artifact_handoff"} {
		t.Run(scenario, func(t *testing.T) {
			// Given: API-created steps block so their live limits can be read.
			f := newArtifactE2E(t)
			names := []string{"producer"}
			if scenario == "artifact_handoff" {
				names = append(names, "consumer")
			}
			for index, name := range names {
				script := "printf payload > \"$DURPDEPLOY_STAGE_DIR/data\"\n"
				if name == "consumer" {
					script = "test \"$(cat \"$DURPDEPLOY_STAGE_DIR/data\")\" = payload\n"
				}
				script += fmt.Sprintf(`mkfifo /tmp/finish
echo %s-ready
read -r result < /tmp/finish
test "$result" = finish
echo %s-complete
`, name, name)
				f.api(t, "POST", f.base()+"/steps", map[string]any{
					"name": name, "sort_order": index,
					"script_body":     "set -eu\n" + script,
					"container_image": "docker.io/library/bash:5.2",
				}, 201)
			}
			// When: the public API runs the release and each step.
			release := verificationRelease(t, f, "resources")
			deployment := verificationDeploy(t, f, release)
			path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
			for _, name := range names {
				waitVerificationLog(t, f, path, name+"-ready")
				ids := strings.Fields(stagingRuntime(t, "ps", "--quiet",
					"--filter=label=io.durpdeploy.namespace="+
						os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")+":"+
						os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE"),
					"--filter=label=io.durpdeploy.attempt"))
				if len(ids) != 1 {
					t.Fatalf("running step containers=%v", ids)
				}
				// Then: no RAM/CPU ceiling, with PID/root isolation retained.
				raw := stagingRuntime(t, "inspect",
					"--format={{json .HostConfig}}", ids[0])
				var limits struct {
					Memory         int64
					NanoCPUs       int64
					CPUQuota       int64
					PidsLimit      int64
					ReadonlyRootfs bool
				}
				if err := json.Unmarshal([]byte(raw), &limits); err != nil {
					t.Fatal(err)
				}
				t.Logf("%s runtime limits: %+v", name, limits)
				if limits.Memory != 0 || limits.NanoCPUs != 0 ||
					limits.CPUQuota > 0 {
					t.Fatalf("step resource ceilings: %+v", limits)
				}
				if limits.PidsLimit != 128 || !limits.ReadonlyRootfs {
					t.Fatalf("step isolation changed: %+v", limits)
				}
				stagingRuntime(t, "exec", "--detach", ids[0], "sh", "-c",
					"printf 'finish\\n' > /tmp/finish")
			}
			f.completion(t, deployment.ID, events.DeploymentSucceeded)
			logs := f.api(t, "GET", path+"/logs", nil, 200)
			if !strings.Contains(
				string(logs),
				names[len(names)-1]+"-complete",
			) {
				t.Fatalf("step did not finish: %s", logs)
			}
		})
	}
}
