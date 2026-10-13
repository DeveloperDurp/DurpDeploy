package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPRImageScanReadsExportedOCILayout(t *testing.T) {
	// Given an OCI archive, the scan input must expose its original index.
	release := readWorkflow(t, "release")
	directory := t.TempDir()
	archiveDir := filepath.Join(directory, "release-image")
	fixture := t.TempDir()
	if err := os.Mkdir(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	index := []byte(`{"schemaVersion":2,"manifests":[]}`)
	if err := os.WriteFile(filepath.Join(fixture, "index.json"), index, 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(archiveDir, "image.tar")
	if data, err := exec.Command("tar", "-cf", archive, "-C", fixture, ".").CombinedOutput(); err != nil {
		t.Fatalf("create OCI fixture: %v\n%s", err, data)
	}
	original, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	prepared := false
	for _, step := range release.Jobs["images"].Steps {
		if step.Name == "Prepare OCI scan layout" {
			command := exec.Command("bash", "-euc", step.Run)
			command.Env = append(os.Environ(), "RUNNER_TEMP="+directory)
			if data, err := command.CombinedOutput(); err != nil {
				t.Fatalf("prepare scan layout: %v\n%s", err, data)
			}
			prepared = true
		}
		if strings.HasPrefix(step.Uses, "aquasecurity/trivy-action@") {
			if !prepared {
				t.Fatal("OCI layout must be prepared before scanning")
			}
			input := strings.ReplaceAll(
				step.With["input"],
				"${{ runner.temp }}",
				directory,
			)
			actual, err := os.ReadFile(filepath.Join(input, "index.json"))
			if err != nil || string(actual) != string(index) {
				t.Fatalf("scan input must contain exported OCI index: %v", err)
			}
		}
	}
	retained, err := os.ReadFile(archive)
	if err != nil || string(retained) != string(original) {
		t.Fatal("scan preparation must preserve the retained archive")
	}
}

func TestReleaseVersionSupportsBuildMetadata(t *testing.T) {
	release := readWorkflow(t, "release")
	for _, fixture := range []struct{ tag, image string }{
		{"v1.2.3+build.4", "v1.2.3_build.4"},
		{"v1.2.3-build.4", "v1.2.3-build.4"},
		{"v1.2.3-alpha.1+build.4", "v1.2.3-alpha.1_build.4"},
	} {
		t.Run(fixture.tag, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "output")
			command := exec.Command(
				"bash",
				"-euc",
				release.Jobs["version"].Steps[0].Run,
			)
			command.Env = append(
				os.Environ(),
				"REF_TYPE=tag",
				"REF_NAME="+fixture.tag,
				"GITHUB_OUTPUT="+output,
			)
			if data, err := command.CombinedOutput(); err != nil {
				t.Fatalf("compute release version: %v\n%s", err, data)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{
				"version=" + fixture.image + "\n",
				"chart_version=" + strings.TrimPrefix(fixture.tag, "v") + "\n",
				"is_release=true\n",
			} {
				if !strings.Contains(string(data), expected) {
					t.Errorf("missing %q in %s", expected, data)
				}
			}
		})
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
