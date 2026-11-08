package cicontract

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestSonarPipelineEventsAndDependencies(t *testing.T) {
	data, err := os.ReadFile("../../.gitlab-ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Include []struct {
			Local string
			Rules []rule
		}
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	jobs := pipeline(t)
	for _, fixture := range []struct {
		name            string
		env             []string
		coverage, sonar bool
	}{
		{"main", nil, true, true},
		{"renamed-default", []string{"CI_DEFAULT_BRANCH=trunk", "CI_COMMIT_BRANCH=trunk"}, true, true},
		{"mr", []string{"CI_PIPELINE_SOURCE=merge_request_event"}, true, true},
		{"fork-mr", []string{"CI_PIPELINE_SOURCE=merge_request_event", "CI_MERGE_REQUEST_SOURCE_PROJECT_ID=4"}, true, false},
		{"fork-main", []string{"CI_PROJECT_PATH=someone/durpdeploy", "CI_PROJECT_ID=4"}, false, false},
		{"feature", []string{"CI_COMMIT_BRANCH=feature"}, false, false},
		{"tag", []string{"CI_COMMIT_BRANCH=", "CI_COMMIT_TAG=v1.0.0"}, false, false},
		{"web", []string{"CI_PIPELINE_SOURCE=web"}, false, false},
		{"schedule", []string{"CI_PIPELINE_SOURCE=schedule"}, false, false},
		{"security-web", []string{"CI_PIPELINE_SOURCE=web", "HTTPS_SECURITY_ONLY=true"}, false, false},
		{"security-schedule", []string{"CI_PIPELINE_SOURCE=schedule", "HTTPS_SECURITY_ONLY=true"}, false, false},
		{"cleanup", []string{"CI_PIPELINE_SOURCE=schedule", "CI_CACHE_CLEANUP=true"}, false, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			env := append([]string{
				"CI_PIPELINE_SOURCE=push", "CI_COMMIT_BRANCH=main",
				"CI_DEFAULT_BRANCH=main", "CI_PROJECT_ID=3",
				"CI_PROJECT_PATH=durfy/apps/durpdeploy",
				"CI_MERGE_REQUEST_SOURCE_PROJECT_ID=3",
			}, fixture.env...)
			included := map[string]bool{}
			for _, include := range root.Include {
				addIncludedJobs(t, include.Local, include.Rules, env, included)
			}
			included["ci-runtime"] = rulesEnabled(
				t,
				jobs["ci-runtime"].Rules,
				env,
			)
			for name, want := range map[string]bool{
				"coverage": fixture.coverage, "go-test-cache": fixture.coverage,
				"sonarcloud": fixture.sonar,
			} {
				checkSonarEventJob(t, name, want, jobs, included, env)
			}
		})
	}
	for _, name := range []string{"coverage", "sonarcloud", "go-test-cache"} {
		if jobs[name].allowsFailure() {
			t.Errorf("%s must block failures", name)
		}
	}
	if jobs["sonarcloud"].Environment.Name != "sonar" ||
		!jobs["sonarcloud"].needs("coverage") {
		t.Fatal("scanner must use the existing credential scope and coverage")
	}
}

func addIncludedJobs(
	t *testing.T,
	path string,
	rules []rule,
	env []string,
	included map[string]bool,
) {
	t.Helper()
	if !rulesEnabled(t, rules, env) {
		return
	}
	content, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	var names map[string]yaml.Node
	if err := yaml.Unmarshal(content, &names); err != nil {
		t.Fatal(err)
	}
	for name := range names {
		included[name] = true
	}
}

func checkSonarEventJob(
	t *testing.T,
	name string,
	want bool,
	jobs map[string]job,
	included map[string]bool,
	env []string,
) {
	t.Helper()
	got := included[name] && rulesEnabled(t, jobs[name].Rules, env)
	if got != want {
		t.Errorf("%s enabled=%t, want %t", name, got, want)
	}
	if !got {
		return
	}
	for _, dependency := range jobs[name].Needs {
		if !included[dependency.Job] ||
			!rulesEnabled(t, jobs[dependency.Job].Rules, env) {
			t.Errorf("%s needs excluded %s", name, dependency.Job)
		}
	}
}

// These rules use equality and Boolean operators shared by GitLab and Bash.
// Keep the fixture environment empty so host CI variables cannot affect it.
func rulesEnabled(t *testing.T, rules []rule, env []string) bool {
	t.Helper()
	for _, rule := range rules {
		if rule.If != "" {
			cmd := exec.Command("bash", "-c", "[[ "+rule.If+" ]]")
			cmd.Env = env
			if output, err := cmd.CombinedOutput(); err != nil {
				if exit, ok := err.(*exec.ExitError); !ok ||
					exit.ExitCode() != 1 {
					t.Fatalf("rule %s: %v: %s", rule.If, err, output)
				}
				continue
			}
		}
		return rule.When != "never"
	}
	return rules == nil
}

func TestGoCacheEventIsolation(t *testing.T) {
	var cache goBuildCache
	producer := pipeline(t)["go-test-cache"]
	if err := producer.Cache.Decode(&cache); err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, env := range []map[string]string{
		{"CI_PROJECT_ID": "3", "CI_COMMIT_REF_SLUG": "main", "CI_COMMIT_REF_PROTECTED": "true"},
		{"CI_PROJECT_ID": "3", "CI_COMMIT_REF_SLUG": "main", "CI_COMMIT_REF_PROTECTED": "false"},
		{"CI_PROJECT_ID": "3", "CI_COMMIT_REF_SLUG": "feature", "CI_COMMIT_REF_PROTECTED": "false", "CI_MERGE_REQUEST_SOURCE_PROJECT_ID": "3", "CI_MERGE_REQUEST_IID": "64"},
		{"CI_PROJECT_ID": "3", "CI_COMMIT_REF_SLUG": "feature", "CI_COMMIT_REF_PROTECTED": "false", "CI_MERGE_REQUEST_SOURCE_PROJECT_ID": "4", "CI_MERGE_REQUEST_IID": "64"},
	} {
		key := os.Expand(
			cache.Key,
			func(name string) string { return env[name] },
		)
		if slices.Contains(keys, key) {
			t.Fatalf("cache contexts collide: %s", key)
		}
		keys = append(keys, key)
	}
}

func TestSonarFailurePropagates(t *testing.T) {
	dir := t.TempDir()
	coverageReportFixture(
		t,
		dir,
		[]string{
			"api-1",
			"api-2",
			"handlers",
			"repository",
			"migrations",
			"core",
		},
		"",
	)
	path := fixtureCommand(t, "sonar-scanner", "exit 42\n")
	cmd := exec.Command(
		"bash",
		"-euo",
		"pipefail",
		"-c",
		strings.Join(pipeline(t)["sonarcloud"].Script, "\n"),
	)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + path + ":" + os.Getenv("PATH"),
		"SONAR_TOKEN=cicontract-fixture",
		"CI_PIPELINE_SOURCE=push",
		"CI_COMMIT_BRANCH=main",
	}
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("scanner failure was ignored: %s", output)
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 42 {
		t.Fatalf("want scanner failure 42, got %v: %s", err, output)
	}
}
