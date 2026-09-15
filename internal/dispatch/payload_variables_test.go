package dispatch_test

import (
	"testing"

	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/runner"
)

func TestVariableSnapshotsPreservesResolvedSecretMetadata(t *testing.T) {
	variables := dispatch.VariableSnapshots([]runner.ResolvedVariable{{Name: "TOKEN", Value: "redacted", Secret: true}})
	if len(variables) != 1 || variables[0].Name != "TOKEN" || variables[0].Value != "redacted" || !variables[0].Secret {
		t.Fatalf("snapshots = %+v", variables)
	}
}
