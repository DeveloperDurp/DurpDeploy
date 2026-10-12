//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestDeploymentStagingPackageAPIE2E(t *testing.T) {
	s := newStagingE2E(t)
	for _, scenario := range []string{"handoff", "agent-first", "identical", "conflict", "failure", "cancel", "changed-package", "slow-package"} {
		t.Run(
			scenario,
			func(t *testing.T) { s.checkPackageScenario(t, scenario) },
		)
	}
}

func (s *stagingE2E) checkPackageScenario(t *testing.T, scenario string) {
	t.Helper()
	base, environmentID := s.packageProject(t, scenario)
	workspace := s.packageSource(
		t,
		packageSourceConfig{base: base, scenario: scenario},
	)
	s.addPackageScenarioSteps(
		t,
		packageSourceConfig{base: base, scenario: scenario},
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
	files, err := os.ReadDir(workspace)
	if err != nil || len(files) != 1 || files[0].Name() != ".lock" {
		t.Fatalf("server package files retained: %v %v", files, err)
	}
}
