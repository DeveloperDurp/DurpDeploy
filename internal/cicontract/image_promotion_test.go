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
	prepare, prepareStep := findWorkflowStep(
		t,
		release.Jobs["images"],
		"Prepare OCI scan layout",
	)
	scan, scanStep := findWorkflowStep(
		t,
		release.Jobs["images"],
		"Scan image archive",
	)
	if scan <= prepare {
		t.Fatal("OCI layout must be prepared before scanning")
	}
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
	command := exec.Command("bash", "-euc", prepareStep.Run)
	command.Env = append(os.Environ(), "RUNNER_TEMP="+directory)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare scan layout: %v\n%s", err, data)
	}
	if !strings.Contains(
		scanStep.Run,
		`scripts/container_scan.sh image "$RUNNER_TEMP/image-scan-layout"`,
	) {
		t.Fatal("scan must read the prepared OCI layout")
	}
	input := filepath.Join(directory, "image-scan-layout")
	actual, err := os.ReadFile(filepath.Join(input, "index.json"))
	if err != nil || string(actual) != string(index) {
		t.Fatalf("scan input must contain exported OCI index: %v", err)
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
				release.Jobs["version"].Steps[1].Run,
			)
			command.Dir = "../.."
			command.Env = append(
				os.Environ(),
				"GITHUB_REF_TYPE=tag",
				"GITHUB_REF_NAME="+fixture.tag,
				"BUILD_MODIFIED=false",
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
				"software_version=" + fixture.tag + "\n",
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

func TestPRImageBuildExportsStampedArchive(t *testing.T) {
	images := readWorkflow(t, "release").Jobs["images"]
	if images.If != "" || !slices.Contains(images.Needs, "version") {
		t.Fatal("all builds must scan images with computed build metadata")
	}
	_, build := findWorkflowStep(t, images, "Build image archive")
	if !strings.HasPrefix(build.Uses, "docker/build-push-action@") {
		t.Fatal("image archive must use the configured OCI builder")
	}
	for _, expected := range []string{
		"BUILD_VERSION=${{ needs.version.outputs.software_version }}",
		"BUILD_NUMBER=${{ needs.version.outputs.build_number }}",
		"BUILD_COMMIT=${{ needs.version.outputs.commit }}",
	} {
		if !strings.Contains(build.With["build-args"], expected) {
			t.Errorf("image build omitted %s", expected)
		}
	}
	if build.With["push"] != "false" ||
		!strings.HasPrefix(build.With["outputs"], "type=oci,") {
		t.Fatal("PR builds must export OCI without publishing")
	}
}

func TestImageScanPrecedesArchiveRetention(t *testing.T) {
	images := readWorkflow(t, "release").Jobs["images"]
	build, _ := findWorkflowStep(t, images, "Build image archive")
	scan, scanStep := findWorkflowStep(t, images, "Scan image archive")
	retain, retainStep := findWorkflowStep(t, images, "Retain scanned image")
	if !strings.Contains(
		scanStep.Run,
		`bash scripts/container_scan.sh image "$RUNNER_TEMP/image-scan-layout" "$RUNNER_TEMP/image-evidence"`,
	) ||
		scanStep.If != "" {
		t.Fatal("archive scan must block on findings or errors")
	}
	if retainStep.If != "" {
		t.Fatal("artifact retention must require successful scans")
	}
	if scan <= build || retain <= scan {
		t.Fatal("images must be built, scanned, then retained")
	}
}

func TestImagePublicationUsesVerifiedPRArtifacts(t *testing.T) {
	// Publication requires verified PR source and this run's scanned image bytes.
	release := readWorkflow(t, "release")
	if !slices.Contains(release.Jobs["docker"].Needs, "images") {
		t.Fatal("publication must require this build's scanned images")
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
			t.Fatal("publisher must not rebuild or rescan verified images")
		}
	}
	if !slices.Contains(release.Jobs["helm"].Needs, "docker") {
		t.Fatal("chart publication must require successful image publication")
	}
	if release.Jobs["docker"].Concurrency.Group != "release-image-publication" {
		t.Fatal("image publishers must share one serialization group")
	}
	if release.Jobs["docker"].Permissions["pull-requests"] != "read" {
		t.Fatal("PR association lookup requires pull-requests read permission")
	}
}
