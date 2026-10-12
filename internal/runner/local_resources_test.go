package runner

import (
	"os"
	"strings"
	"testing"
)

func TestLocalAttemptLeavesRAMAndCPUToRuntime(t *testing.T) {
	for _, kind := range []string{"docker", "podman"} {
		for _, staged := range []bool{false, true} {
			name := kind + "/ordinary"
			if staged {
				name = kind + "/artifact_handoff"
			}
			t.Run(name, func(t *testing.T) {
				// Given: a step client for either runtime and optional handoff.
				r, repo, trace := podmanFixture(t, "")
				r.engine.kind = kind
				client := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in
    --remote|--host=*|--url=*) shift;;
    *) break;;
  esac
done
case "$1" in
  image) printf 'null\n';;
  inspect) printf 'true\n';;
  run) printf '%s\n' "$@" > "$PODMAN_TRACE";;
esac
`
				if err := os.WriteFile(r.engine.binary, []byte(client), 0o700); err != nil {
					t.Fatal(err)
				}
				request := localStepAttempt{
					step: deploymentStep{
						ContainerImage: "example/worker:1",
					},
					logWriter: attemptLogWriter(t, r, repo, 1),
				}
				if staged {
					request.handoff = artifactStage{
						volume: "handoff", keeper: "handoff-keeper",
					}
				}
				// When: the runtime receives the container launch request.
				if err := r.runStepAttempt(t.Context(), request); err != nil {
					t.Fatal(err)
				}
				// Then: RAM/CPU policy is external; other isolation remains.
				args, err := os.ReadFile(trace)
				if err != nil {
					t.Fatal(err)
				}
				for _, arg := range strings.Split(string(args), "\n") {
					if strings.HasPrefix(arg, "--memory") ||
						strings.HasPrefix(arg, "--cpu") {
						t.Errorf("unexpected resource limit: %s", arg)
					}
				}
				for _, flag := range []string{
					"--pids-limit=128", "--read-only", "--cap-drop=ALL",
					"--security-opt=no-new-privileges", "--user=65534:65534",
					"--network=none", "--tmpfs=/tmp:rw,nosuid,size=64m",
				} {
					if !strings.Contains(string(args), flag+"\n") {
						t.Errorf("missing isolation option: %s", flag)
					}
				}
			})
		}
	}
}
