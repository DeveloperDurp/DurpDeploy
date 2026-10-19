package cicontract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runtimeImageFixture(t *testing.T, env ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("../../scripts/ci_build_runtime.sh")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	bin := fixtureCommand(t, "docker", `
set -eu
printf '%s\n' "$*" >> "$FIXTURE_CALLS"
case $1 in
  info|logout) ;;
  login) cat >/dev/null ;;
  buildx)
    if [ "$2" != build ]; then exit 0; fi
    while [ "$#" -gt 0 ]; do
      if [ "$1" = --metadata-file ]; then metadata=$2; break; fi
      shift
    done
    digest=$FIXTURE_DIGEST
    case $metadata in
      */browser.json) digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb ;;
    esac
    printf '{"containerimage.digest":"%s"}\n' "$digest" > "$metadata"
    ;;
  *) exit 1 ;;
esac
`)
	command := exec.Command("bash", script)
	command.Dir = directory
	command.Env = append(metadataEnvironment(),
		"PATH="+bin+":"+os.Getenv("PATH"), "GITLAB_CI=true",
		"CI_REGISTRY_IMAGE=registry.example/project",
		"CI_REGISTRY=registry.example", "CI_REGISTRY_USER=fixture",
		"CI_REGISTRY_PASSWORD=fixture-only", "CI_PIPELINE_ID=42",
		"CI_PROJECT_ID=3", "FIXTURE_CALLS="+directory+"/calls",
		"FIXTURE_DIGEST=sha256:"+strings.Repeat("a", 64))
	command.Env = append(command.Env, env...)
	output, err := command.CombinedOutput()
	t.Logf("runtime build output: %s", output)
	return directory, err
}

func TestRuntimeImageReportPinsThePushedDigest(t *testing.T) {
	for _, digest := range []string{
		"sha256:" + strings.Repeat("a", 64), "mutable", "sha256:invalid", "",
	} {
		t.Run(digest, func(t *testing.T) {
			directory, err := runtimeImageFixture(t, "FIXTURE_DIGEST="+digest)
			valid := digest == "sha256:"+strings.Repeat("a", 64)
			if valid != (err == nil) {
				t.Fatalf("digest %q: %v", digest, err)
			}
			if !valid {
				if _, err := os.Stat(filepath.Join(directory,
					"artifacts/runtime.env")); !os.IsNotExist(err) {
					t.Fatal("invalid digest produced a runtime report")
				}
				return
			}
			data, err := os.ReadFile(filepath.Join(directory,
				"artifacts/runtime.env"))
			want := "CI_RUNTIME_IMAGE=registry.example/project/ci@" + digest + "\n"
			if err != nil || string(data) != want {
				t.Fatalf("runtime dotenv: %s, %v", data, err)
			}
		})
	}
}

func TestRuntimeCachesRemainInsideTheirMergeRequest(t *testing.T) {
	for _, fixture := range []struct {
		name, event, source, protected, iid string
		cached, valid                       bool
	}{
		{"same-project", "merge_request_event", "3", "false", "64", true, true},
		{"different-mr", "merge_request_event", "3", "false", "65", true, true},
		{"fork", "merge_request_event", "4", "false", "64", false, true},
		{"protected", "merge_request_event", "3", "true", "64", false, true},
		{"main", "push", "3", "true", "", false, true},
		{"manual", "web", "3", "false", "", false, true},
		{"invalid-id", "merge_request_event", "3", "false", "../main", false, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			directory, err := runtimeImageFixture(t,
				"CI_PIPELINE_SOURCE="+fixture.event,
				"CI_MERGE_REQUEST_SOURCE_PROJECT_ID="+fixture.source,
				"CI_COMMIT_REF_PROTECTED="+fixture.protected,
				"CI_MERGE_REQUEST_IID="+fixture.iid)
			if fixture.valid != (err == nil) {
				t.Fatalf("runtime build: %v", err)
			}
			if !fixture.valid {
				return
			}
			checkRuntimeCacheCalls(t, directory, fixture.iid, fixture.cached)
		})
	}
}

func checkRuntimeCacheCalls(t *testing.T, directory, iid string, cached bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	calls := string(data)
	if strings.Contains(calls, "--cache-from") != cached ||
		strings.Contains(calls, "--cache-to") != cached {
		t.Fatalf("unexpected cache policy: %s", calls)
	}
	for _, name := range []string{"browser", "runtime"} {
		if cached && !strings.Contains(calls,
			"ref=registry.example/project/ci-cache/mr-"+iid+":"+name) {
			t.Fatalf("cache escaped MR scope: %s", calls)
		}
	}
	want := "BROWSER_IMAGE=registry.example/project/ci-browser@sha256:" +
		strings.Repeat("b", 64)
	if !strings.Contains(calls, want) {
		t.Fatalf("helper build did not pin browser digest: %s", calls)
	}
}
