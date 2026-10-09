package cicontract

import (
	"slices"
	"strings"
	"testing"
)

func TestTestWorkflowsRunOnlyOnPullRequests(t *testing.T) {
	for _, name := range []string{"ci", "sonarcloud"} {
		t.Run(name, func(t *testing.T) {
			config := readWorkflow(t, name)
			if _, ok := config.On["pull_request"]; !ok ||
				len(config.On) != 1 {
				t.Fatal("test workflow must trigger only on pull requests")
			}
		})
	}
}

func TestReleaseTestsRunOnlyOnPullRequests(t *testing.T) {
	release := readWorkflow(t, "release")
	for _, name := range []string{"test", "e2e"} {
		job, ok := release.Jobs[name]
		if !ok || job.If != "github.event_name == 'pull_request'" {
			t.Errorf("%s must run only on pull requests", name)
		}
	}
}

func TestSecurityTestsRunOnlyOnPullRequests(t *testing.T) {
	security := readWorkflow(t, "security")
	for _, command := range []string{
		"make security-scan-test",
		"go test -race -count=1 -run " +
			"'^TestAPITokenTelemetryE2E$' ./internal/handler/api",
	} {
		found := false
		for _, step := range security.Jobs["scan"].Steps {
			if step.Run == command {
				found = true
				if !strings.Contains(
					step.If,
					"github.event_name == 'pull_request' &&",
				) {
					t.Errorf("test step must require a PR: %s", command)
				}
			}
		}
		if !found {
			t.Errorf("security test step is missing: %s", command)
		}
	}
}

func TestPublishersRequireChecksThatRunOutsidePullRequests(t *testing.T) {
	release := readWorkflow(t, "release")
	for _, name := range []string{"docker", "helm"} {
		t.Run(name, func(t *testing.T) {
			publisher, ok := release.Jobs[name]
			if !ok {
				t.Fatal("publisher is missing")
			}
			for _, required := range []string{"version", "lint", "security"} {
				if !slices.Contains(publisher.Needs, required) {
					t.Errorf("publisher must require %s", required)
				}
			}
			var checkDependencies func(string)
			checkDependencies = func(name string) {
				job, ok := release.Jobs[name]
				if !ok || job.If != "" {
					t.Fatalf(
						"publishing depends on missing/conditional job %s",
						name,
					)
				}
				for _, dependency := range job.Needs {
					checkDependencies(dependency)
				}
			}
			for _, dependency := range publisher.Needs {
				checkDependencies(dependency)
			}
		})
	}
}
