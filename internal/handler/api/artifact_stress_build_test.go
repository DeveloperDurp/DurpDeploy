//go:build e2e && artifactstress && linux

package api_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildArtifactServer(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "durpdeploy")
	module := exec.CommandContext(
		t.Context(),
		"go",
		"list",
		"-m",
		"-f",
		"{{.Dir}}",
		"github.com/DeveloperDurp/durpdeploy-agent",
	)
	moduleDirectory, err := module.Output()
	if err != nil {
		t.Fatal(err)
	}
	moduleCopy := filepath.Join(directory, "agent")
	if err := os.CopyFS(
		moduleCopy,
		os.DirFS(strings.TrimSpace(string(moduleDirectory))),
	); err != nil {
		t.Fatal(err)
	}
	modfile := filepath.Join(directory, "go.mod")
	for _, name := range []string{"go.mod", "go.sum"} {
		contents, err := os.ReadFile(filepath.Join("../../..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(directory, name),
			contents,
			0600,
		); err != nil {
			t.Fatal(err)
		}
	}
	edit := exec.CommandContext(
		t.Context(),
		"go",
		"mod",
		"edit",
		"-modfile",
		modfile,
		"-replace",
		"github.com/DeveloperDurp/durpdeploy-agent="+moduleCopy,
	)
	if output, err := edit.CombinedOutput(); err != nil {
		t.Fatalf("prepare test module: %v\n%s", err, output)
	}
	source := filepath.Join(
		moduleCopy,
		"artifact",
		"zip.go",
	)
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	anchor := "total += count"
	if strings.Count(string(contents), anchor) != 1 {
		t.Fatal("extraction barrier insertion point changed")
	}
	instrumented := strings.Replace(
		string(contents),
		anchor,
		anchor+"\nif err := artifactExtractionCrashGate(root); err != nil { return err }",
		1,
	)
	zipSource := filepath.Join(directory, "zip.go")
	gateSource := filepath.Join(directory, "gate.go")
	if err := os.WriteFile(zipSource, []byte(instrumented), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		gateSource,
		[]byte(artifactExtractionGateSource),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(
		struct{ Replace map[string]string }{
			map[string]string{
				source: zipSource,
				filepath.Join(filepath.Dir(source), "artifact_crash_gate.go"): gateSource,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(directory, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	// This test binary alone gets the extraction barrier. No production hook,
	// runtime configuration, dependency, or repository source is added.
	command := exec.CommandContext(
		t.Context(),
		"go",
		"build",
		"-modfile",
		modfile,
		"-overlay",
		overlayPath,
		"-o",
		binary,
		"../../../cmd/server",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build test server: %v\n%s", err, output)
	}
	return binary
}

const artifactExtractionGateSource = `package artifact

import (
    "io"
    "os"
)

func artifactExtractionCrashGate(root *os.Root) error {
    if root == nil || os.Getenv("DURPDEPLOY_ARTIFACT_TEST_PHASE") != "extraction" {
        return nil
    }
    if err := os.Unsetenv("DURPDEPLOY_ARTIFACT_TEST_PHASE"); err != nil {
        return err
    }
    if err := os.WriteFile(os.Getenv("DURPDEPLOY_ARTIFACT_TEST_EXTRACTION_READY"), []byte("ready"), 0600); err != nil {
        return err
    }
    release, err := os.Open(os.Getenv("DURPDEPLOY_ARTIFACT_TEST_EXTRACTION_RELEASE"))
    if err != nil { return err }
    defer release.Close()
    _, err = io.ReadFull(release, make([]byte, 1))
    return err
}
`
