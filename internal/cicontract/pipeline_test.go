package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type rule struct {
	If string `yaml:"if"`
}

type need struct {
	Job       string `yaml:"job"`
	Artifacts *bool  `yaml:"artifacts"`
}

func (n *need) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&n.Job)
	}
	type fields need
	return node.Decode((*fields)(n))
}

type job struct {
	Extends       string    `yaml:"extends"`
	Stage         string    `yaml:"stage"`
	Needs         []need    `yaml:"needs"`
	Rules         []rule    `yaml:"rules"`
	Script        []string  `yaml:"script"`
	Cache         yaml.Node `yaml:"cache"`
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
		Exclude  []string `yaml:"exclude"`
	} `yaml:"artifacts"`
}

func (j job) needs(name string) bool {
	return slices.ContainsFunc(j.Needs, func(n need) bool {
		return n.Job == name
	})
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
		if current.Cache.Kind == 0 {
			current.Cache = parent.Cache
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
			if !current.needs(required) {
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
	if !jobs["publish-images"].needs("retain-image") ||
		!jobs["publish-chart"].needs("publish-images") {
		t.Fatal("publishers bypass scanned-image or image-publication gates")
	}
}

func TestImageRetentionUsesPublicationRules(t *testing.T) {
	jobs := pipeline(t)
	retention := jobs["retain-image"].Rules
	publication := jobs["publish-images"].Rules
	if len(retention) != 1 || len(publication) != 1 ||
		retention[0].If != publication[0].If {
		t.Fatal("image retention must run exactly when images can be published")
	}
}

func TestMergeRequestJobsBlockPipeline(t *testing.T) {
	jobs := pipeline(t)
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
			!current.needs("lint") {
			t.Errorf("%s must be a blocking MR test after lint", name)
		}
	}
}

func TestPipelineFailurePolicies(t *testing.T) {
	jobs := pipeline(t)
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
		if jobs[name].allowsFailure() || jobs[name].When == "always" {
			t.Errorf("%s must block pipeline success", name)
		}
	}
}

func TestPipelineDependenciesExist(t *testing.T) {
	jobs := pipeline(t)
	for name, current := range jobs {
		for _, dependency := range current.Needs {
			if _, ok := jobs[dependency.Job]; !ok {
				t.Errorf("%s needs missing job %s", name, dependency.Job)
			}
		}
	}
}

func TestServerE2EScenariosRunOnceAcrossJobs(t *testing.T) {
	command := regexp.MustCompile(`(?m)^\s*` +
		`(?:DURPDEPLOY_RUNBOOK_BROWSER_E2E=1\s+)?` +
		`bash (?:\./)?scripts/e2e_server_containers\.sh` +
		`(?:[ \t]+([a-z-]+))?[ \t]*$`)
	owners := map[string]string{}
	for name, current := range pipeline(t) {
		scenarios := command.FindAllStringSubmatch(
			strings.Join(current.Script, "\n"), -1,
		)
		if len(scenarios) > 1 {
			t.Errorf("%s serializes independent server scenarios", name)
		}
		for _, match := range scenarios {
			scenario := match[1]
			checkServerE2EScenario(t, name, current, scenario, match[0])
			if previous, ok := owners[scenario]; ok {
				t.Errorf("%s repeats %s from %s", name, scenario, previous)
			}
			owners[scenario] = name
		}
	}
	for _, scenario := range []string{"existing-server", "host", "container"} {
		if _, ok := owners[scenario]; !ok {
			t.Errorf("missing server E2E scenario %s", scenario)
		}
	}
}

func checkServerE2EScenario(
	t *testing.T, name string, current job, scenario, command string,
) {
	t.Helper()
	if !slices.Contains([]string{
		"existing-server", "host", "container",
	}, scenario) {
		t.Fatalf("%s runs an unknown or combined scenario %q", name, scenario)
	}
	if !current.needs("images") || current.allowsFailure() {
		t.Errorf("%s must use the scanned image and block failures", name)
	}
	if scenario != "existing-server" && !strings.Contains(
		command, "DURPDEPLOY_RUNBOOK_BROWSER_E2E=1",
	) {
		t.Errorf("%s omits the runbook browser checks", name)
	}
}

func TestServerE2ERejectsInvalidSelection(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {""}, {"host", "container"}} {
		cmd := exec.Command("bash", append([]string{
			"../../scripts/e2e_server_containers.sh",
		}, args...)...)
		cmd.Env = append(os.Environ(), "GITLAB_CI=true")
		output, err := cmd.CombinedOutput()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 {
			t.Fatalf("args=%q: want selection error, got %v: %s",
				args, err, output)
		}
		if !strings.Contains(string(output), "E2E scenario") {
			t.Fatalf("args=%q: selection was not validated: %s", args, output)
		}
	}
}

func TestEvidenceRetentionAndAMD64Coverage(t *testing.T) {
	jobs := pipeline(t)
	for name, expiry := range map[string]string{
		"images": "4 hours", "security-scan": "7 days",
		"container-security": "7 days",
	} {
		current := jobs[name]
		if current.allowsFailure() || current.Artifacts.When != "always" ||
			current.Artifacts.ExpireIn != expiry || len(current.Rules) != 0 {
			t.Errorf("%s must block and retain evidence on every event", name)
		}
	}
	retained := jobs["retain-image"].Artifacts
	if retained.When != "on_success" || retained.ExpireIn != "4 hours" ||
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

func TestArtifactDownloadsOnlyReachConsumers(t *testing.T) {
	jobs := pipeline(t)
	consumers := map[string][]string{
		".mr-built-app":          {"version", "images"},
		"e2e":                    {"version", "images"},
		"auth-mfa-sqlite":        {"version", "images"},
		"dev-stacks":             {"version", "images"},
		"agent-container-docker": {"agent-image"},
		"agent-container-podman": {"agent-image"},
		"images":                 {"version"},
		"retain-image":           {"images"},
		"publish-images":         {"version", "retain-image"},
		"publish-chart":          {"version"},
		"sonarcloud":             {"coverage"},
	}
	for name, current := range jobs {
		for _, dependency := range current.Needs {
			want := dependency.Job == "ci-runtime" ||
				slices.Contains(consumers[name], dependency.Job)
			actual := dependency.Artifacts == nil || *dependency.Artifacts
			if actual != want {
				t.Errorf("%s downloads %s: got %t, want %t",
					name, dependency.Job, actual, want)
			}
		}
	}
}

type goBuildCache struct {
	Key    string   `yaml:"key"`
	Policy string   `yaml:"policy"`
	Paths  []string `yaml:"paths"`
}

func TestGoCacheHasOneWriterAndPullOnlyConsumers(t *testing.T) {
	jobs := pipeline(t)
	producer := jobs["go-test-cache"]
	var cache goBuildCache
	if err := producer.Cache.Decode(&cache); err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{
		"$CI_PROJECT_ID", "$CI_MERGE_REQUEST_SOURCE_PROJECT_ID",
		"$CI_MERGE_REQUEST_IID",
	} {
		if !strings.Contains(cache.Key, variable) {
			t.Errorf("cache key does not isolate %s", variable)
		}
	}
	if cache.Policy != "pull-push" || producer.allowsFailure() ||
		len(producer.Artifacts.Paths) != 0 ||
		!slices.Equal(cache.Paths, []string{".cache/ci-go/"}) {
		t.Fatal("Go cache producer must use cache storage without artifacts")
	}
	for name, current := range jobs {
		if name != "go-test-cache" && name != "lint" &&
			current.Cache.Kind == yaml.MappingNode {
			checkGoCacheConsumer(t, name, current, cache)
		}
		if current.needs("lint") && len(current.Rules) == 1 &&
			current.Rules[0].If == `$CI_PIPELINE_SOURCE == "merge_request_event"` &&
			!current.needs("go-test-cache") {
			t.Errorf("%s can start without the compiled test handoff", name)
		}
	}
}

func TestLintCacheIsIsolatedFromTestAndProtectedCaches(t *testing.T) {
	lint := pipeline(t)["lint"]
	var cache struct {
		Key struct {
			Prefix string
			Files  []string
		}
		Paths  []string
		Policy string
		When   string
	}
	if err := lint.Cache.Decode(&cache); err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{
		"$CI_PROJECT_ID", "$CI_MERGE_REQUEST_SOURCE_PROJECT_ID",
		"$CI_MERGE_REQUEST_IID", "$CI_COMMIT_REF_PROTECTED",
		"$CI_COMMIT_REF_SLUG", "$GOTOOLCHAIN",
	} {
		if !strings.Contains(cache.Key.Prefix, variable) {
			t.Errorf("lint cache key does not isolate %s", variable)
		}
	}
	if !slices.Equal(cache.Paths, []string{".cache/golangci-lint/"}) ||
		cache.Policy != "pull-push" || cache.When != "on_success" ||
		lint.needs("go-test-cache") {
		t.Fatal(
			"lint must own a separate cache without waiting for test builds",
		)
	}
	if !slices.Equal(cache.Key.Files, []string{
		"scripts/install_golangci_lint.sh", ".golangci.yml",
	}) {
		t.Fatal("lint cache must invalidate on tool and configuration changes")
	}
}

func checkGoCacheConsumer(
	t *testing.T, name string, current job, expected goBuildCache,
) {
	t.Helper()
	var consumer goBuildCache
	if err := current.Cache.Decode(&consumer); err != nil {
		t.Fatal(err)
	}
	if consumer.Policy != "pull" || consumer.Key != expected.Key ||
		!slices.Equal(consumer.Paths, expected.Paths) ||
		!current.needs("go-test-cache") || len(current.Rules) != 1 ||
		current.Rules[0].If != `$CI_PIPELINE_SOURCE == "merge_request_event"` {
		t.Errorf("%s must only pull its MR cache after the producer", name)
	}
}

func TestConsumersDoNotUploadDownloadedArchives(t *testing.T) {
	data, err := os.ReadFile("../../.gitlab-ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Default job `yaml:"default"`
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"artifacts/built-image-amd64/**/*", "artifacts/agent-build/**/*",
		"artifacts/runtime.env", "artifacts/runtime-timing.json",
		"artifacts/build.env", "artifacts/image.env",
	} {
		if !slices.Contains(root.Default.Artifacts.Exclude, path) {
			t.Errorf("consumers re-upload %s", path)
		}
	}
	if root.Default.Artifacts.ExpireIn != "1 day" {
		t.Fatal("diagnostics must expire after one day")
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
