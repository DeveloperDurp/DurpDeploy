package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestFuzzJobIsBoundedAndRestricted(t *testing.T) {
	data, err := os.ReadFile("../../.gitlab/ci/test.yml")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Fuzz struct {
			Timeout   string                 `yaml:"timeout"`
			Variables map[string]string      `yaml:"variables"`
			Rules     []map[string]yaml.Node `yaml:"rules"`
		} `yaml:"fuzz"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	j := pipeline(t)["fuzz"]
	if j.Stage != "test" || config.Fuzz.Timeout != "20 minutes" ||
		!j.needs("ci-runtime") || j.allowsFailure() ||
		j.Environment.Name != "" || j.When != "" || len(j.Rules) != 3 ||
		len(config.Fuzz.Rules[0]) != 1 ||
		j.Rules[0].If != `$CI_PIPELINE_SOURCE == "merge_request_event"` ||
		j.Rules[1].If != `$CI_PIPELINE_SOURCE == "schedule"` ||
		j.Rules[2].If != `$CI_PIPELINE_SOURCE == "web" && $CI_FUZZ == "true"` {
		t.Fatal("fuzz job bypasses trigger, deadline, or failure constraints")
	}
	for key, want := range map[string]string{
		"CI_RUNTIME": "none", "KUBERNETES_CPU_LIMIT": "2",
		"KUBERNETES_MEMORY_LIMIT": "4Gi", "GOMEMLIMIT": "1GiB",
	} {
		if config.Fuzz.Variables[key] != want {
			t.Fatalf(
				"fuzz resource setting %s=%s",
				key,
				config.Fuzz.Variables[key],
			)
		}
	}
	if j.Artifacts.When != "always" || j.Artifacts.ExpireIn != "7 days" ||
		strings.Join(j.Artifacts.Paths, " ") != "artifacts/fuzz/" ||
		!strings.Contains(strings.Join(j.Script, "\n"), "ci_fuzz.sh") {
		t.Fatal("fuzz failures do not retain bounded evidence")
	}
}

func TestFuzzHarnessSelectionIsolationAndFailures(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail],
			func(t *testing.T) { checkFuzzHarness(t, fail) })
	}
}

func checkFuzzHarness(t *testing.T, fail bool) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../scripts/ci_fuzz.sh")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scripts", "ci_fuzz.sh")
	if err := os.WriteFile(path, script, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := fixtureCommand(t, "go", `printf '%s\n' "$HOME/cache"`)
	bin += ":" + fixtureCommand(t, "timeout", `
printf '%s\n' "$*" >> "$HOME/deadlines"
shift 3
exec "$@"
`)
	bin += ":" + fixtureCommand(t, "bash", `
[ -z "${SONAR_TOKEN+x}${DURPDEPLOY_SECRET_KEY+x}${DOCKER_HOST+x}" ] || exit 91
printf '%s\n' "$*" >> "$HOME/commands"
printf 'private-input-should-not-be-retained\nfuzz: elapsed: 0s, execs: 1\n'
case "$*" in *FuzzReadJSON*)
  if [ -f "$HOME/fail" ]; then
    mkdir -p internal/handler/testdata/fuzz/FuzzReadJSON
    printf 'synthetic-reproducer' > internal/handler/testdata/fuzz/FuzzReadJSON/abc
    exit 9
  fi;;
esac
`)
	if fail {
		if err := os.WriteFile(filepath.Join(dir, "fail"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(), "HOME="+dir,
		"PATH="+bin+":"+os.Getenv("PATH"), "SONAR_TOKEN=synthetic",
		"DURPDEPLOY_SECRET_KEY=synthetic", "DOCKER_HOST=synthetic")
	output, runErr := cmd.CombinedOutput()
	if (runErr != nil) != fail {
		t.Fatalf("failure propagation: %v: %s", runErr, output)
	}
	if strings.Contains(string(output), "private-input") {
		t.Fatal("raw inputs reached the job trace")
	}
	checkFuzzCommands(t, dir)
	if fail {
		corpus, err := os.ReadFile(filepath.Join(dir,
			"artifacts/fuzz/corpus/FuzzReadJSON/abc"))
		if err != nil || string(corpus) != "synthetic-reproducer" {
			t.Fatal("failed input was not retained exactly")
		}
	}
	checkFuzzReports(t, dir)
}

func checkFuzzReports(t *testing.T, dir string) {
	t.Helper()
	logs, err := filepath.Glob(filepath.Join(dir, "artifacts/fuzz/*.log"))
	if err != nil || len(logs) != 5 {
		t.Fatal("missing fuzz reports")
	}
	for _, log := range logs {
		data, err := os.ReadFile(log)
		if err != nil || strings.Contains(string(data), "private-input") ||
			!strings.Contains(string(data), "Reproduce:") {
			t.Fatal("unsafe or unreproducible fuzz evidence")
		}
	}
}

func checkFuzzCommands(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "commands"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 5 {
		t.Fatalf("selected %d fuzz commands", len(lines))
	}
	for index, target := range []string{
		"FuzzReadJSON", "FuzzValidateZIPPaths",
		"FuzzScrubberStreaming", "FuzzDefaultPatternStreaming",
		"FuzzReviewJSONSyntax",
	} {
		for _, required := range []string{
			"-run ^$", "-fuzz ^" + target + "$", "-fuzztime=30s",
			"-fuzzminimizetime=5s", "-parallel=2", "-timeout=2m",
		} {
			if !strings.Contains(lines[index], required) {
				t.Fatalf("%s misses %s", target, required)
			}
		}
	}
	data, err = os.ReadFile(filepath.Join(dir, "deadlines"))
	if err != nil || strings.Count(string(data),
		"--signal=TERM --kill-after=10s 6m env -i") != 5 {
		t.Fatal("process deadline or clean environment missing")
	}
}
