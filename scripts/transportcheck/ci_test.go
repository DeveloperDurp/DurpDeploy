package main

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

type ciRule struct {
	If   string `yaml:"if"`
	When string `yaml:"when"`
}

type ciJob struct {
	Rules     []ciRule `yaml:"rules"`
	Needs     []string `yaml:"needs"`
	Artifacts struct {
		Paths []string `yaml:"paths"`
	} `yaml:"artifacts"`
}

func TestTransportScanOnlyPipelineCannotIncludePublication(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".gitlab-ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var pipeline struct {
		Include []struct {
			Local string   `yaml:"local"`
			Rules []ciRule `yaml:"rules"`
		} `yaml:"include"`
		Runtime ciJob `yaml:"ci-runtime"`
		Publish ciJob `yaml:".publish"`
	}
	if err := yaml.Unmarshal(data, &pipeline); err != nil {
		t.Fatal(err)
	}
	blocked := func(rules []ciRule) bool {
		return len(rules) > 0 &&
			rules[0].If == `$HTTPS_SECURITY_ONLY == "true"` &&
			rules[0].When == "never"
	}
	found := make(map[string]bool)
	for _, include := range pipeline.Include {
		found[include.Local] = true
		if include.Local == ".gitlab/ci/cache-cleanup.yml" &&
			(len(include.Rules) != 2 || include.Rules[1].If != "" ||
				include.Rules[1].When != "always") {
			t.Fatal("scan-only rules must preserve cache-cleanup schedules")
		}
		if include.Local == ".gitlab/ci/security.yml" {
			if blocked(include.Rules) {
				t.Fatal("scan-only pipeline excluded its verification jobs")
			}
		} else if !blocked(include.Rules) {
			t.Fatal("scan-only pipeline can include unrelated or publishing jobs")
		}
	}
	if !found[".gitlab/ci/security.yml"] || !found[".gitlab/ci/release.yml"] ||
		!blocked(pipeline.Publish.Rules) || blocked(pipeline.Runtime.Rules) {
		t.Fatal(
			"scan-only pipeline is missing its publication barrier or runtime",
		)
	}
}

func TestTransportScanOnlyPipelineRetainsEvidenceAndExcludesCredentials(
	t *testing.T,
) {
	data, err := os.ReadFile(
		filepath.Join("..", "..", ".gitlab", "ci", "security.yml"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var jobs map[string]ciJob
	if err := yaml.Unmarshal(data, &jobs); err != nil {
		t.Fatal(err)
	}
	job, ok := jobs["transport-security"]
	if !ok || len(job.Needs) != 1 || job.Needs[0] != "ci-runtime" ||
		len(
			job.Artifacts.Paths,
		) != 1 || job.Artifacts.Paths[0] != "artifacts/transport/" {
		t.Fatal("transport job must retain only its sanitized evidence")
	}
	for _, name := range []string{"security-scan", "container-security"} {
		rules := jobs[name].Rules
		if len(rules) == 0 || rules[0].When != "never" ||
			rules[0].If != `$HTTPS_SECURITY_ONLY == "true"` {
			t.Fatal("scan-only pipeline admitted an unrelated scanner")
		}
	}
	var raw map[string]map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["transport-security"]["environment"]; ok {
		t.Fatal(
			"transport checks must not select credential-scoped environments",
		)
	}
}
