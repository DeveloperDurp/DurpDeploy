package cicontract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAgentArchiveRejectsWrongIdentityAndTampering(t *testing.T) {
	script, err := filepath.Abs("../../scripts/ci_agent_image.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{
		"", "commit", "pipeline", "job", "version", "sum", "image", "image.tar", "agent",
	} {
		t.Run("changed="+changed, func(t *testing.T) {
			directory := agentArchiveFixture(t, changed)
			bin := fixtureCommand(t, "go", `
case "$*" in
  *Version*) echo v1.2.3 ;;
  *Sum*) echo h1:fixture ;;
  *) exit 1 ;;
esac
`)
			bin = fixtureCommand(t, "podman", `
printf '%s\n' "$*" >> "$CALLS"
`) + ":" + bin
			calls := filepath.Join(directory, "calls")
			command := exec.Command("bash", script, "load")
			command.Dir = directory
			command.Env = append(
				os.Environ(),
				"PATH="+bin+":"+os.Getenv("PATH"),
				"GITLAB_CI=true", "AGENT_BUILD_JOB_ID=43",
				"CI_COMMIT_SHA=fixture-commit",
				"CI_PIPELINE_ID=42",
				"DURPDEPLOY_CONTAINER_RUNTIME=podman",
				"CALLS="+calls,
			)
			output, err := command.CombinedOutput()
			valid := changed == ""
			_, calledErr := os.Stat(calls)
			if valid != (err == nil) || valid != (calledErr == nil) {
				t.Fatalf("archive valid=%t result=%v runtime=%v: %s",
					valid, err, calledErr, output)
			}
		})
	}
}

func agentArchiveFixture(t *testing.T, changed string) string {
	t.Helper()
	directory := t.TempDir()
	artifacts := filepath.Join(directory, "artifacts", "agent-build")
	if err := os.MkdirAll(artifacts, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("fixture")
	identity := map[string]any{
		"commit": "fixture-commit", "pipeline": 42, "job": 43,
		"version": "v1.2.3", "sum": "h1:fixture",
		"image":   "localhost/durpdeploy-agent:gitlab-42",
		"archive": fmt.Sprintf("%x", sha256.Sum256(content)),
		"binary":  fmt.Sprintf("%x", sha256.Sum256(content)),
	}
	for _, file := range []string{"image.tar", "agent"} {
		data := content
		if changed == file {
			data = []byte("tampered")
		}
		if err := os.WriteFile(filepath.Join(artifacts, file),
			data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := identity[changed]; ok {
		identity[changed] = "wrong"
	}
	data, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, "source.json"),
		data, 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}
