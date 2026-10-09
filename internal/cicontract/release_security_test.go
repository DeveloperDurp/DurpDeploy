package cicontract

import (
	"os"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type workflow struct {
	On          map[string]yaml.Node `yaml:"on"`
	Concurrency struct {
		Group string `yaml:"group"`
	} `yaml:"concurrency"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Uses            string   `yaml:"uses"`
	Needs           []string `yaml:"needs"`
	If              string   `yaml:"if"`
	ContinueOnError bool     `yaml:"continue-on-error"`
	Steps           []struct {
		Run string `yaml:"run"`
		If  string `yaml:"if"`
	} `yaml:"steps"`
}

func readWorkflow(t *testing.T, name string) workflow {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/" + name + ".yml")
	if err != nil {
		t.Fatal(err)
	}
	var result workflow
	if err := yaml.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestEveryPublisherRequiresSuccessfulSecurityScan(t *testing.T) {
	release := readWorkflow(t, "release")
	security := readWorkflow(t, "security")
	if _, ok := security.On["workflow_call"]; !ok {
		t.Error(
			"security workflow cannot run inside the release dependency graph",
		)
	}
	scan, ok := release.Jobs["security"]
	if !ok || scan.Uses != "./.github/workflows/security.yml" ||
		scan.If != "" || scan.ContinueOnError {
		t.Error(
			"release must unconditionally call the same-commit security workflow",
		)
	}
	for _, publisher := range []string{"docker", "helm"} {
		t.Run(publisher, func(t *testing.T) {
			assertPublisherRequiresSecurity(t, release.Jobs[publisher])
		})
	}
	var push struct {
		Branches []string `yaml:"branches"`
		Tags     []string `yaml:"tags"`
	}
	pushNode, ok := release.On["push"]
	if !ok {
		t.Fatal("release push trigger is missing")
	}
	if err := pushNode.Decode(&push); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(push.Branches, "main") ||
		!slices.Contains(push.Tags, "*") {
		t.Fatal("main and all release tags must enter the security-gated graph")
	}
}

func assertPublisherRequiresSecurity(t *testing.T, job workflowJob) {
	t.Helper()
	if !slices.Contains(job.Needs, "security") {
		t.Fatal("publisher can run without a completed security scan")
	}
	// GitHub implicitly requires success when no status function is used.
	// These overrides could publish despite a failed/cancelled dependency.
	for _, override := range []string{"always(", "failure(", "cancelled("} {
		if strings.Contains(job.If, override) {
			t.Fatalf("publisher bypasses successful dependencies: %s", override)
		}
	}
}

func TestCalledAndStandaloneScansCannotCancelEachOther(t *testing.T) {
	security := readWorkflow(t, "security")
	group := security.Concurrency.Group
	if !strings.Contains(group, "${{ github.ref }}") {
		t.Fatal("scan concurrency must remain scoped to its ref")
	}
	standalone := strings.ReplaceAll(
		group,
		"${{ github.workflow }}",
		"Security gates",
	)
	called := strings.ReplaceAll(group, "${{ github.workflow }}", "release")
	if standalone == called ||
		called == readWorkflow(t, "release").Concurrency.Group {
		t.Fatal(
			"standalone/called scans can cancel a scan or its release caller",
		)
	}
}
