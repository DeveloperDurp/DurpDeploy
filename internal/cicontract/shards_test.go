package cicontract

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fixtureCommand(t *testing.T, name, body string) string {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, name),
		[]byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestAgentCIContractFollowsDispatcher(t *testing.T) {
	cmd := exec.Command("bash", "scripts/check-agent-ci-contract_test.sh")
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}

func TestCoveragePartitionsEveryPackageOnce(t *testing.T) {
	checkPackagePartition(t, "ci_coverage_packages.sh",
		[]string{"api", "handlers", "repository", "migrations", "core"})
}

func TestCoverageIncludesEveryDefaultTestFile(t *testing.T) {
	files := func(tags string) map[string]bool {
		t.Helper()
		cmd := exec.Command("go", "list", "-json", "-tags="+tags, "./...")
		cmd.Dir = "../.."
		output, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(strings.NewReader(string(output)))
		result := map[string]bool{}
		for {
			var pkg struct {
				ImportPath                string
				TestGoFiles, XTestGoFiles []string
			}
			if err := decoder.Decode(&pkg); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			for _, file := range append(pkg.TestGoFiles, pkg.XTestGoFiles...) {
				result[pkg.ImportPath+"/"+file] = true
			}
		}
		return result
	}
	coverage := files("e2e")
	defaults := files("")
	if len(defaults) == 0 {
		t.Fatal("default test inventory is empty")
	}
	for file := range defaults {
		if !coverage[file] {
			t.Errorf("coverage suite omits default test file %s", file)
		}
	}
}

func checkPackagePartition(t *testing.T, script string, shards []string) {
	t.Helper()
	packages := []string{
		"durpdeploy/internal/handler/api",
		"durpdeploy/internal/handler",
		"durpdeploy/internal/agentserver",
		"durpdeploy/internal/repository",
		"durpdeploy/internal/migrate",
		"durpdeploy/cmd/server",
		"durpdeploy/internal/newpackage",
	}
	path := fixtureCommand(t, "go", `
if [ "$GO_LIST_FAIL" = 1 ]; then exit 9; fi
printf '%s\n' "$CI_TEST_PACKAGES"
`)
	seen := map[string]int{}
	for _, shard := range shards {
		output, err := packageShardCommand(
			path,
			script,
			shard,
			packages,
			"0",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", shard, err, output)
		}
		for _, name := range strings.Fields(string(output)) {
			seen[name]++
		}
	}
	for _, name := range packages {
		if seen[name] != 1 {
			t.Errorf("%s selected %d times", name, seen[name])
		}
	}
	if len(seen) != len(packages) {
		t.Fatalf("unexpected package inventory: %v", seen)
	}
	for _, shard := range []string{"unknown", "core"} {
		cmd := packageShardCommand(path, script, shard, packages, "1")
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("%s accepted failed selection: %s", shard, output)
		}
	}
}

func packageShardCommand(
	path, script, shard string,
	packages []string,
	fail string,
) *exec.Cmd {
	cmd := exec.Command("bash", "../../scripts/"+script, shard)
	cmd.Env = append(os.Environ(), "PATH="+path+":"+os.Getenv("PATH"),
		"GO_LIST_FAIL="+fail, "CI_TEST_PACKAGES="+strings.Join(packages, "\n"))
	return cmd
}

func TestTestShardsPropagateFailure(t *testing.T) {
	path := fixtureCommand(t, "bash", "exit 9\n")
	path = fixtureCommand(t, "make", "exit 9\n") + ":" + path
	for _, shard := range []string{
		"rollout-contracts", "rollout-contracts-1", "rollout-contracts-2",
		"rollout-artifacts-1", "rollout-artifacts-2", "rollout-verification",
		"browser-navigation", "browser-resources", "browser-permissions", "browser-execution",
		"browser-agents", "unknown",
	} {
		cmd := exec.Command("bash", "../../scripts/ci_test_shard.sh", shard)
		cmd.Env = append(os.Environ(), "PATH="+path+":"+os.Getenv("PATH"))
		output, err := cmd.CombinedOutput()
		want := 9
		if shard == "unknown" {
			want = 2
		}
		if exit, ok := err.(*exec.ExitError); !ok ||
			exit.ExitCode() != want {
			t.Fatalf("%s: exit=%v want=%d: %s", shard, err, want, output)
		}
	}
}

func TestRolloutContractsPreserveEveryCommandAndFlag(t *testing.T) {
	// Captured from the original contracts shard before splitting its commands.
	data, err := os.ReadFile("testdata/rollout_contract_commands.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	for decoder.More() {
		var call struct {
			Command string
			Args    []string
			Runtime string
		}
		if err := decoder.Decode(&call); err != nil {
			t.Fatal(err)
		}
		want = append(want, fmt.Sprintf("%s %s|runtime=%s",
			call.Command, strings.Join(call.Args, " "), call.Runtime))
	}
	if len(want) != 14 {
		t.Fatalf("original contract inventory has %d commands", len(want))
	}
	body := `printf '%s %s|runtime=%s\n' "${0##*/}" "$*" "${DURPDEPLOY_CONTAINER_RUNTIME:-}" >> "$FIXTURE_COMMANDS"`
	path := fixtureCommand(
		t,
		"bash",
		body,
	) + ":" + fixtureCommand(
		t,
		"make",
		body,
	)
	commands := filepath.Join(t.TempDir(), "commands")
	shards := pipeline(t)["agent-rollout"].Parallel.Matrix[0]["ROLLOUT_SHARD"]
	count := 0
	for _, shard := range shards {
		if !strings.HasPrefix(shard, "contracts-") {
			continue
		}
		count++
		cmd := exec.Command(
			"bash",
			"../../scripts/ci_test_shard.sh",
			"rollout-"+shard,
		)
		cmd.Env = append(os.Environ(), "PATH="+path+":"+os.Getenv("PATH"),
			"FIXTURE_COMMANDS="+commands, "DURPDEPLOY_CONTAINER_RUNTIME=")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", shard, err, output)
		}
	}
	if count != 2 {
		t.Fatalf("want two contract groups, got %d", count)
	}
	data, err = os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("contract commands changed:\ngot %q\nwant %q", got, want)
	}
}

func TestSonarRequiresEveryCoverageReport(t *testing.T) {
	shards := []string{
		"api-1",
		"api-2",
		"handlers",
		"repository",
		"migrations",
		"core",
	}
	script := strings.Join(pipeline(t)["sonarcloud"].Script, "\n")
	path := fixtureCommand(t, "sonar-scanner",
		"printf '%s\\n' \"$@\" > \"$SCANNER_ARGS\"\n")
	for _, missing := range append([]string{""}, shards...) {
		t.Run("missing="+missing, func(t *testing.T) {
			dir := t.TempDir()
			coverageReportFixture(t, dir, shards, missing)
			args := filepath.Join(dir, "scanner-args")
			cmd := exec.Command("bash", "-euo", "pipefail", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+path+":"+os.Getenv("PATH"),
				"SCANNER_ARGS="+args, "SONAR_TOKEN=cicontract-fixture",
				"CI_MERGE_REQUEST_IID=64",
				"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME=improvement/gitlab-ci",
				"CI_MERGE_REQUEST_TARGET_BRANCH_NAME=main")
			output, err := cmd.CombinedOutput()
			checkSonarReportResult(t, missing, args, output, err, shards)
		})
	}
}

func coverageReportFixture(
	t *testing.T,
	dir string,
	shards []string,
	missing string,
) {
	t.Helper()
	coverage := filepath.Join(dir, "artifacts", "coverage")
	if err := os.MkdirAll(coverage, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, shard := range shards {
		if shard == missing {
			continue
		}
		if err := os.WriteFile(filepath.Join(coverage, shard+".out"),
			[]byte("mode: atomic\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func checkSonarReportResult(
	t *testing.T,
	missing, args string,
	output []byte,
	runErr error,
	shards []string,
) {
	t.Helper()
	if missing != "" {
		if runErr == nil {
			t.Fatalf("missing %s was accepted: %s", missing, output)
		}
		if _, err := os.Stat(args); !os.IsNotExist(err) {
			t.Fatal("scanner ran with missing coverage")
		}
		return
	}
	if runErr != nil {
		t.Fatalf("%v: %s", runErr, output)
	}
	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "-Dsonar.qualitygate.wait=true") {
		t.Fatal("scanner does not wait for its quality gate")
	}
	for _, shard := range shards {
		if !strings.Contains(string(data), "artifacts/coverage/"+shard+".out") {
			t.Fatalf("scanner omits %s", shard)
		}
	}
}
