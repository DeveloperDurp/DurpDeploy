package runner

import (
	"fmt"
	"os"
	"testing"
)

func TestLocalAttemptPreservesLegacyArtifactPathWithoutPackage(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		t.Run(fmt.Sprint(restricted), func(t *testing.T) {
			// Given: legacy snapshot data, not a new variable write.
			r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{}';;
ps|rm|volume) ;;
run) printf '%s' "$ARTIFACT_PATH" > "$PODMAN_TRACE";;
esac
`)
			names := []string{}
			if restricted {
				names = []string{"ARTIFACT_PATH"}
			}
			// When
			err := r.runStepAttempt(t.Context(), localStepAttempt{
				step: deploymentStep{
					ContainerImage: "example.com/worker:1",
					VariableNames:  names,
				},
				logWriter: &broadcastWriter{
					ctx:      t.Context(),
					repo:     repo,
					broker:   r.broker,
					scrubber: NewScrubber(nil),
				},
				environment: map[string]string{
					"ARTIFACT_PATH": "/legacy/package",
				},
			})
			// Then
			if err != nil {
				t.Fatal(err)
			}
			value, err := os.ReadFile(trace)
			if err != nil || string(value) != "/legacy/package" {
				t.Fatalf("legacy variable changed: %q %v", value, err)
			}
		})
	}
}

func TestMountedArtifactStillReservesSnapshotVariable(t *testing.T) {
	// Given / When
	request := localStepAttempt{artifact: artifactStage{volume: "package"}}
	allowed := request.validExecutionVariable("ARTIFACT_PATH")
	// Then
	if allowed {
		t.Fatal("mounted package accepted a snapshot override")
	}
}
