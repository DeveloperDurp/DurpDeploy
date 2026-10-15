package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildMetadataCountsSourceCommits(t *testing.T) {
	script, err := filepath.Abs("../../scripts/build_metadata.sh")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = directory
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, data)
		}
		return strings.TrimSpace(string(data))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "build@example.invalid")
	git("config", "user.name", "Build fixture")
	git("commit", "-q", "--allow-empty", "-m", "first")
	read := func(env ...string) map[string]string {
		t.Helper()
		command := exec.Command("bash", script)
		command.Dir = directory
		command.Env = append(metadataEnvironment(), env...)
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("metadata: %v\n%s", err, data)
		}
		result := map[string]string{}
		for line := range strings.SplitSeq(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				result[key] = value
			}
		}
		return result
	}
	first := read()
	if first["build_number"] != "1" ||
		first["software_version"] != "v0.0.0-dev.1+g"+git(
			"rev-parse",
			"--short=12",
			"HEAD",
		) {
		t.Fatalf("first build = %v", first)
	}
	git("commit", "-q", "--allow-empty", "-m", "second")
	second := read("GITHUB_REF_TYPE=branch")
	if second["build_number"] != "2" ||
		second["version"] == first["version"] ||
		second["version"] != read("GITHUB_REF_TYPE=branch")["version"] {
		t.Fatalf("repeated source identity = %v", second)
	}
	git("tag", "v1.2.3+build.4")
	tag := read("GITHUB_REF_TYPE=tag", "GITHUB_REF_NAME=v1.2.3+build.4")
	if tag["software_version"] != "v1.2.3+build.4" ||
		tag["version"] != "v1.2.3_build.4" || tag["is_release"] != "true" ||
		tag["commit"] != git("rev-parse", "HEAD") {
		t.Fatalf("tag identity = %v", tag)
	}
	if read("GITHUB_REF_TYPE=branch")["software_version"] !=
		second["software_version"] {
		t.Fatal("branch build at tagged commit was presented as a release")
	}
	if err := os.WriteFile(filepath.Join(directory, "untracked"),
		[]byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty := read()
	if dirty["modified"] != "true" ||
		!strings.HasSuffix(dirty["software_version"], ".dirty") {
		t.Fatalf("modified tag checkout = %v", dirty)
	}
	for _, invalid := range []string{
		"v01.2.3", "v1.2.3-01", "v1.2.3+", "v1.2.3\nversion=bad",
	} {
		command := exec.Command("bash", script)
		command.Dir = directory
		command.Env = append(metadataEnvironment(),
			"BUILD_MODIFIED=false", "GITHUB_REF_TYPE=tag",
			"GITHUB_REF_NAME="+invalid)
		if data, err := command.CombinedOutput(); err == nil {
			t.Fatalf("accepted invalid tag %q: %s", invalid, data)
		}
	}
	shallow := filepath.Join(t.TempDir(), "shallow")
	clone := exec.Command("git", "clone", "-q", "--depth=1",
		"file://"+directory, shallow)
	if data, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("shallow clone: %v\n%s", err, data)
	}
	command := exec.Command("bash", script)
	command.Dir = shallow
	command.Env = append(metadataEnvironment(), "GITHUB_REF_TYPE=branch")
	data, err := command.CombinedOutput()
	if err != nil ||
		!strings.Contains(string(data), "build_number=unknown\n") ||
		!strings.Contains(string(data), "software_version=dev\n") {
		t.Fatalf("shallow checkout invented a count: %s error=%v", data, err)
	}
	command = exec.Command("bash", script)
	command.Dir = t.TempDir()
	command.Env = metadataEnvironment()
	data, err = command.CombinedOutput()
	if err != nil || !strings.Contains(string(data), "commit=unknown\n") ||
		!strings.Contains(string(data), "software_version=dev\n") {
		t.Fatalf("source archive invented metadata: %s error=%v", data, err)
	}
}

func metadataEnvironment() []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "BUILD_") &&
			!strings.HasPrefix(value, "GITHUB_") {
			env = append(env, value)
		}
	}
	return env
}
