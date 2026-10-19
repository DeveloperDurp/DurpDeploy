package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type rule struct {
	If string `yaml:"if"`
}

type job struct {
	Extends       string    `yaml:"extends"`
	Stage         string    `yaml:"stage"`
	Needs         []string  `yaml:"needs"`
	Rules         []rule    `yaml:"rules"`
	Script        []string  `yaml:"script"`
	AllowFailure  yaml.Node `yaml:"allow_failure"`
	When          string    `yaml:"when"`
	Interruptible *bool     `yaml:"interruptible"`
	ResourceGroup string    `yaml:"resource_group"`
	Environment   struct {
		Name string `yaml:"name"`
	} `yaml:"environment"`
	Parallel struct {
		Matrix []map[string][]string `yaml:"matrix"`
	} `yaml:"parallel"`
	Artifacts struct {
		When     string   `yaml:"when"`
		ExpireIn string   `yaml:"expire_in"`
		Paths    []string `yaml:"paths"`
	} `yaml:"artifacts"`
}

func (j job) allowsFailure() bool {
	return j.AllowFailure.Kind != 0 &&
		(j.AllowFailure.Tag != "!!bool" || j.AllowFailure.Value != "false")
}

func pipeline(t *testing.T) map[string]job {
	t.Helper()
	paths := []string{"../../.gitlab-ci.yml"}
	includes, err := filepath.Glob("../../.gitlab/ci/*.yml")
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, includes...)
	jobs := map[string]job{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var nodes map[string]yaml.Node
		if err := yaml.Unmarshal(data, &nodes); err != nil {
			t.Fatal(err)
		}
		for name, node := range nodes {
			if slices.Contains([]string{
				"include", "workflow", "stages", "variables", "default",
			}, name) {
				continue
			}
			var current job
			if err := node.Decode(&current); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			jobs[name] = current
		}
	}
	resolved := map[string]job{}
	resolving := map[string]bool{}
	var resolve func(string) job
	resolve = func(name string) job {
		if current, ok := resolved[name]; ok {
			return current
		}
		if resolving[name] {
			t.Fatalf("cyclic job inheritance at %s", name)
		}
		current, ok := jobs[name]
		if !ok {
			t.Fatalf("missing job %s", name)
		}
		resolving[name] = true
		if current.Extends == "" {
			resolved[name] = current
			return current
		}
		parent := resolve(current.Extends)
		if current.Rules == nil {
			current.Rules = parent.Rules
		}
		if current.AllowFailure.Kind == 0 {
			current.AllowFailure = parent.AllowFailure
		}
		if current.Needs == nil {
			current.Needs = parent.Needs
		}
		if current.Interruptible == nil {
			current.Interruptible = parent.Interruptible
		}
		if current.ResourceGroup == "" {
			current.ResourceGroup = parent.ResourceGroup
		}
		if current.Environment.Name == "" {
			current.Environment = parent.Environment
		}
		resolved[name] = current
		return current
	}
	for name := range jobs {
		resolve(name)
	}
	return resolved
}

func TestPublishersRequireSameCommitSecurityAndScannedImages(t *testing.T) {
	jobs := pipeline(t)
	for _, name := range []string{"publish-images", "publish-chart"} {
		current := jobs[name]
		for _, required := range []string{
			"version", "lint", "security-scan", "container-security",
		} {
			if !slices.Contains(current.Needs, required) {
				t.Errorf("%s can bypass %s", name, required)
			}
		}
		if current.allowsFailure() || current.When == "always" ||
			current.Interruptible == nil || *current.Interruptible ||
			current.Environment.Name != "production" ||
			current.ResourceGroup != "release-publication" {
			t.Errorf("%s violates the protected publication boundary", name)
		}
		if len(current.Rules) != 1 ||
			!strings.Contains(current.Rules[0].If,
				`CI_COMMIT_REF_PROTECTED == "true"`) ||
			strings.Contains(current.Rules[0].If, "merge_request_event") ||
			strings.Contains(current.Rules[0].If, "schedule") {
			t.Errorf("%s can publish from an untrusted event", name)
		}
	}
	if !slices.Contains(jobs["publish-images"].Needs, "retain-image") ||
		!slices.Contains(jobs["publish-chart"].Needs, "publish-images") {
		t.Fatal("publishers bypass scanned-image or image-publication gates")
	}
}

func TestMergeRequestGateRequiresEveryTestJob(t *testing.T) {
	jobs := pipeline(t)
	gate := jobs["image-verification"]
	for name, current := range jobs {
		if strings.HasPrefix(name, ".") {
			continue
		}
		parent := current.Extends
		for parent != "" && parent != ".mr-test" {
			parent = jobs[parent].Extends
		}
		if parent == "" {
			continue
		}
		if (name != "agent-container-podman" && current.allowsFailure()) ||
			len(current.Rules) != 1 ||
			current.Rules[0].If !=
				`$CI_PIPELINE_SOURCE == "merge_request_event"` ||
			!slices.Contains(current.Needs, "lint") {
			t.Errorf("%s must be a blocking MR test after lint", name)
		}
		if !slices.Contains(gate.Needs, name) {
			t.Errorf("image verification omits %s", name)
		}
	}
	var podmanPolicy struct {
		ExitCodes int `yaml:"exit_codes"`
	}
	policy := jobs["agent-container-podman"].AllowFailure
	if policy.Kind != yaml.MappingNode || len(policy.Content) != 2 ||
		policy.Decode(&podmanPolicy) != nil || podmanPolicy.ExitCodes != 77 {
		t.Fatal("only the disabled user-namespace preflight may allow failure")
	}
	for _, name := range []string{
		"retain-image", "security-scan", "container-security",
	} {
		if !slices.Contains(gate.Needs, name) {
			t.Errorf("image verification omits %s", name)
		}
	}
	for name, current := range jobs {
		for _, dependency := range current.Needs {
			if _, ok := jobs[dependency]; !ok {
				t.Errorf("%s needs missing job %s", name, dependency)
			}
		}
	}
}

func TestEvidenceRetentionAndAMD64Coverage(t *testing.T) {
	jobs := pipeline(t)
	for _, name := range []string{
		"images", "security-scan", "container-security",
	} {
		current := jobs[name]
		if current.allowsFailure() || current.Artifacts.When != "always" ||
			current.Artifacts.ExpireIn != "7 days" || len(current.Rules) != 0 {
			t.Errorf("%s must block and retain evidence on every event", name)
		}
	}
	retained := jobs["retain-image"].Artifacts
	if retained.When != "on_success" || retained.ExpireIn != "30 days" ||
		!slices.Contains(retained.Paths, "artifacts/built-image-amd64/") {
		t.Fatal("only successful scans can produce promotion artifacts")
	}
	data, err := os.ReadFile("../../scripts/ci_image.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if !strings.Contains(script, "--platform linux/amd64") ||
		strings.Contains(script, "arm64") ||
		strings.Index(script, "container_scan.sh image") >
			strings.Index(script, `mv "$private/image.tar"`) {
		t.Fatal("promotion must follow the AMD64 scan")
	}
}

func TestRootlessNamespacePreflightExit(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "cat"),
		[]byte("#!/bin/sh\nprintf '0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		runtime string
		exit    int
	}{{"podman", 77}, {"none", 1}} {
		t.Run(fixture.runtime, func(t *testing.T) {
			cmd := exec.Command("bash", "../../scripts/ci_rootless.sh")
			cmd.Env = append(
				os.Environ(),
				"GITLAB_CI=true",
				"CI_RUNTIME="+fixture.runtime,
				"PATH="+bin+":"+os.Getenv("PATH"),
			)
			output, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok ||
				exit.ExitCode() != fixture.exit {
				t.Fatalf("exit=%v want=%d: %s", err, fixture.exit, output)
			}
		})
	}
}
