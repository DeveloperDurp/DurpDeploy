package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReleaseVersionSupportsBuildMetadata(t *testing.T) {
	release := readWorkflow(t, "release")
	output := filepath.Join(t.TempDir(), "output")
	command := exec.Command(
		"bash",
		"-euc",
		release.Jobs["version"].Steps[0].Run,
	)
	command.Env = append(os.Environ(),
		"REF_TYPE=tag", "REF_NAME=v1.2.3+build.4", "GITHUB_OUTPUT="+output)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compute release version: %v\n%s", err, data)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"version=v1.2.3-build.4\n",
		"chart_version=1.2.3+build.4\n",
		"is_release=true\n",
	} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("missing %q in %s", expected, data)
		}
	}
}

func TestReleaseRunsCannotCancelOtherPublishers(t *testing.T) {
	for _, name := range []string{"release", "security"} {
		group := readWorkflow(t, name).Concurrency.Group
		if !strings.Contains(
			group,
			"${{ github.event_name == 'pull_request' && 'pr' || github.run_id }}",
		) {
			t.Errorf("%s must isolate publishing runs by run ID", name)
		}
	}
}

func TestImagePublicationUsesVerifiedPRArtifacts(t *testing.T) {
	// Given the release graph, publication must consume retained PR images.
	release := readWorkflow(t, "release")
	images := release.Jobs["images"]
	if images.If != "github.event_name == 'pull_request'" {
		t.Fatal("image builds and scans must run on PRs")
	}
	build, scan, retain := -1, -1, -1
	for index, step := range images.Steps {
		switch {
		case strings.HasPrefix(step.Uses, "docker/build-push-action@"):
			build = index
			if step.With["push"] != "false" ||
				!strings.HasPrefix(step.With["outputs"], "type=oci,") {
				t.Fatal("PR builds must export OCI without publishing")
			}
		case strings.HasPrefix(step.Uses, "aquasecurity/trivy-action@"):
			scan = index
			if step.With["exit-code"] != "1" ||
				step.With["severity"] != "HIGH,CRITICAL" ||
				step.With["input"] == "" || step.If != "" {
				t.Fatal("archive scan must block on findings or errors")
			}
		case strings.HasPrefix(step.Uses, "actions/upload-artifact@"):
			retain = index
			if step.If != "" {
				t.Fatal("artifact retention must require successful scans")
			}
		}
	}
	if build < 0 || scan <= build || retain <= scan {
		t.Fatal("images must be built, scanned, then retained")
	}
	gate := release.Jobs["image-verification"]
	for _, dependency := range []string{"images", "test", "e2e", "security"} {
		if !slices.Contains(gate.Needs, dependency) {
			t.Errorf("image gate must require %s", dependency)
		}
	}
	if !strings.Contains(gate.If, "always()") {
		t.Fatal("image gate must report failed or skipped dependencies")
	}
	for _, step := range release.Jobs["docker"].Steps {
		if strings.Contains(step.Uses, "build-push-action") ||
			strings.Contains(step.Uses, "trivy-action") {
			t.Fatal("publisher must not rebuild or rescan PR images")
		}
	}
	if !slices.Contains(release.Jobs["helm"].Needs, "docker") {
		t.Fatal("chart publication must require successful image publication")
	}
	if release.Jobs["docker"].Concurrency.Group != "release-image-publication" {
		t.Fatal("image publishers must share one serialization group")
	}
}
