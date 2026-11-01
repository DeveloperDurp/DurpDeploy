package cicontract

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func inventoryShardCommand(
	t *testing.T,
	script, shard, inventory string,
) *exec.Cmd {
	t.Helper()
	path := fixtureCommand(t, "bash", `
[ "$LIST_FAIL" != 1 ] || exit 9
printf '%s\n' "$TEST_INVENTORY"
`)
	cmd := exec.Command("bash", "../../scripts/"+script, shard)
	cmd.Env = append(os.Environ(), "PATH="+path+":"+os.Getenv("PATH"),
		"LIST_FAIL=0", "TEST_INVENTORY="+inventory)
	return cmd
}

func TestArtifactShardsSelectEveryCompiledTestOnce(t *testing.T) {
	names := []string{
		"TestArtifactGateAlphaE2E",
		"TestArtifactGateBetaE2E",
		"TestArtifactGateFutureE2E",
		"TestArtifactGateOtherE2E",
		"TestArtifactGateÜnicodeE2E",
	}
	counts := map[string]int{}
	for _, shard := range []string{"1", "2"} {
		inventory := "noise\n" + strings.Join(names, "\n") + "\nok package\n"
		output, err := inventoryShardCommand(
			t,
			"ci_artifact_tests.sh",
			shard,
			inventory,
		).CombinedOutput()
		if err != nil {
			t.Fatalf("shard %s: %v: %s", shard, err, output)
		}
		for _, name := range strings.Fields(string(output)) {
			counts[name]++
		}
	}
	if len(counts) != len(names) {
		t.Fatalf("unexpected inventory: %v", counts)
	}
	for _, name := range names {
		if counts[name] != 1 {
			t.Fatalf("%s selected %d times", name, counts[name])
		}
	}
}

func TestArtifactShardsRejectFailedOrEmptyInventories(t *testing.T) {
	for _, fixture := range []struct{ shard, inventory, fail string }{
		{"unknown", "", "0"}, {"1", "noise", "0"},
		{"2", "TestArtifactGateOnlyE2E", "0"},
		{"1", "TestArtifactGatePartialE2E", "1"},
	} {
		cmd := inventoryShardCommand(
			t,
			"ci_artifact_tests.sh",
			fixture.shard,
			fixture.inventory,
		)
		cmd.Env = append(cmd.Env, "LIST_FAIL="+fixture.fail)
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("accepted failed inventory: %s", output)
		}
	}
}

func TestAPICoverageShardsSelectEveryCompiledEntryOnce(t *testing.T) {
	names := []string{
		"Test",
		"TestAlpha",
		"TestÜnicode",
		"Example",
		"ExampleAPI_case",
		"FuzzAPI",
	}
	counts := map[string]int{}
	for _, shard := range []string{"1", "2"} {
		inventory := "noise\nBenchmarkIgnored\n" + strings.Join(
			names,
			"\n",
		) + "\nok package\n"
		output, err := inventoryShardCommand(
			t,
			"ci_coverage_tests.sh",
			shard,
			inventory,
		).CombinedOutput()
		if err != nil {
			t.Fatalf("shard %s: %v: %s", shard, err, output)
		}
		for _, name := range strings.Fields(string(output)) {
			counts[name]++
		}
	}
	if len(counts) != len(names) {
		t.Fatalf("unexpected inventory: %v", counts)
	}
	for _, name := range names {
		if counts[name] != 1 {
			t.Fatalf("%s selected %d times", name, counts[name])
		}
	}
}

func TestAPICoverageShardsRejectFailedOrEmptyInventories(t *testing.T) {
	for _, fixture := range []struct{ shard, inventory, fail string }{
		{"unknown", "", "0"}, {"1", "noise", "0"},
		{"2", "TestOnly", "0"}, {"1", "TestPartial", "1"},
		{"1", "TestDuplicate\nTestDuplicate", "0"},
	} {
		cmd := inventoryShardCommand(
			t,
			"ci_coverage_tests.sh",
			fixture.shard,
			fixture.inventory,
		)
		cmd.Env = append(cmd.Env, "LIST_FAIL="+fixture.fail)
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("accepted failed inventory: %s", output)
		}
	}
}

func TestEveryPackageAndArtifactShardIsScheduled(t *testing.T) {
	jobs := pipeline(t)
	for _, fixture := range []struct {
		job, variable string
		want          []string
	}{
		{"coverage", "COVERAGE_SHARD", []string{"api-1", "api-2", "handlers", "repository", "migrations", "core"}},
		{"package-browser", "BROWSER_SHARD", []string{"navigation", "resources", "permissions", "execution", "agents"}},
		{"agent-rollout", "ROLLOUT_SHARD", []string{"contracts-1", "contracts-2", "artifacts-1", "artifacts-2", "verification"}},
	} {
		var actual []string
		for _, entry := range jobs[fixture.job].Parallel.Matrix {
			actual = append(actual, entry[fixture.variable]...)
		}
		slices.Sort(actual)
		slices.Sort(fixture.want)
		if !slices.Equal(actual, fixture.want) {
			t.Fatalf(
				"%s scheduled shards %v, want %v",
				fixture.job,
				actual,
				fixture.want,
			)
		}
	}
}
