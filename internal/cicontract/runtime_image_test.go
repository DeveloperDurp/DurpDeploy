package cicontract

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
    case $metadata in
      */"${FIXTURE_FAIL_IMAGE:-none}".json) exit 1 ;;
    esac
    printf '{"containerimage.digest":"%s"}\n' "$digest" > "$metadata"
    ;;
  *) exit 1 ;;
esac
`)
	bin = fixtureCommand(t, "curl", `
printf '%s\n' "$*" >> "$FIXTURE_FETCHES"
if [ "${FIXTURE_FETCH_FAIL:-false}" = true ]; then exit 22; fi
response=$FIXTURE_CACHE_JSON
case "$*" in
  *'/pipelines?'*) response=${FIXTURE_PIPELINES_JSON:-'[{"id":41,"project_id":3,"ref":"main","source":"push","status":"success"}]'} ;;
  *'/pipelines/41/jobs?'*) response=${FIXTURE_JOBS_JSON:-'[{"id":99,"name":"ci-runtime","ref":"main","tag":false,"status":"success","pipeline":{"id":41}}]'} ;;
esac
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then printf '%s' "$response" > "$2"; exit 0; fi
  shift
done
exit 1
`) + ":" + bin
	command := exec.Command("bash", script)
	command.Dir = directory
	command.Env = append(metadataEnvironment(),
		"PATH="+bin+":"+os.Getenv("PATH"), "GITLAB_CI=true",
		"CI_REGISTRY_IMAGE=registry.example/project",
		"CI_REGISTRY=registry.example", "CI_REGISTRY_USER=fixture",
		"CI_REGISTRY_PASSWORD=fixture-only", "CI_PIPELINE_ID=42",
		"CI_PROJECT_ID=3", "FIXTURE_CALLS="+directory+"/calls",
		"CI_DEFAULT_BRANCH=main", "CI_JOB_TOKEN=fixture-only",
		"CI_API_V4_URL=https://gitlab.example/api/v4",
		"FIXTURE_FETCHES="+directory+"/fetches",
		"FIXTURE_CACHE_JSON={}",
		"FIXTURE_DIGEST=sha256:"+strings.Repeat("a", 64))
	command.Env = append(command.Env, env...)
	output, err := command.CombinedOutput()
	t.Logf("runtime build output: %s", output)
	return directory, err
}

func TestRuntimeImageBuildFailuresStopReports(t *testing.T) {
	for _, name := range []string{"browser", "runtime"} {
		t.Run(name, func(t *testing.T) {
			directory, err := runtimeImageFixture(t, "FIXTURE_FAIL_IMAGE="+name)
			if err == nil {
				t.Fatal("image build failure was ignored")
			}
			if _, err := os.Stat(filepath.Join(directory,
				"artifacts/runtime.env")); !os.IsNotExist(err) {
				t.Fatal("failed build produced a runtime report")
			}
		})
	}
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
		strings.Contains(calls, "--cache-to") != cached ||
		strings.Contains(calls, "--cache-to type=inline") != cached {
		t.Fatalf("unexpected cache policy: %s", calls)
	}
	for _, name := range []string{"browser", "runtime"} {
		if cached && !strings.Contains(calls,
			"ref=registry.example/project/ci-cache/mr-"+iid+":"+name) {
			t.Fatalf("cache escaped MR scope: %s", calls)
		}
	}
	if cached && (!strings.Contains(calls, "mode=min,ignore-error=true") ||
		strings.Contains(calls, "mode=max")) {
		t.Fatalf("cache exports unused intermediate stages: %s", calls)
	}
	want := "BROWSER_IMAGE=registry.example/project/ci-browser@sha256:" +
		strings.Repeat("b", 64)
	if !strings.Contains(calls, want) {
		t.Fatalf("helper build did not pin browser digest: %s", calls)
	}
}

func runtimeCacheMetadata() map[string]any {
	return map[string]any{
		"project_id":  "3",
		"pipeline_id": "41",
		"ref":         "main",
		"protected":   true,
		"cache_epoch": time.Now().Unix() / 604800,
		"browser": "registry.example/project/ci-browser@sha256:" + strings.Repeat(
			"c",
			64,
		),
		"runtime": "registry.example/project/ci@sha256:" + strings.Repeat(
			"d",
			64,
		),
	}
}

func TestTrustedRuntimeCacheUsesOnlyDefaultBranchArtifactDigests(t *testing.T) {
	for _, fixture := range []struct {
		name, event, source, branch, protected string
		trusted, eligibleMR                    bool
	}{
		{"push", "push", "", "main", "true", true, false},
		{"web", "web", "", "main", "true", true, false},
		{"schedule", "schedule", "", "main", "true", true, false},
		{"merge_request_event", "merge_request_event", "3", "", "false", false, true},
		{"fork", "merge_request_event", "4", "", "false", false, false},
		{"unprotected", "push", "", "main", "false", false, false},
		{"tag", "push", "", "", "true", false, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			metadata, _ := json.Marshal(runtimeCacheMetadata())
			directory, err := runtimeImageFixture(t,
				"FIXTURE_CACHE_JSON="+string(metadata),
				"CI_PIPELINE_SOURCE="+fixture.event,
				"CI_MERGE_REQUEST_SOURCE_PROJECT_ID="+fixture.source,
				"CI_COMMIT_REF_PROTECTED="+fixture.protected,
				"CI_COMMIT_BRANCH="+fixture.branch,
				"CI_MERGE_REQUEST_IID=64")
			if err != nil {
				t.Fatal(err)
			}
			inline := fixture.trusted || fixture.eligibleMR
			refresh := fixture.event == "schedule"
			checkTrustedRuntimeCacheBuild(
				t,
				directory,
				inline && !refresh,
				inline,
				refresh,
			)
			checkTrustedRuntimeCacheReport(t, directory, fixture.trusted)
		})
	}
}

func checkTrustedRuntimeCacheBuild(
	t *testing.T, directory string, wantImport, inline, refresh bool,
) {
	t.Helper()
	calls, err := os.ReadFile(filepath.Join(directory, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"browser", "runtime"} {
		digest := runtimeCacheMetadata()[name].(string)
		if strings.Contains(
			string(calls),
			"--cache-from type=registry,ref="+digest,
		) != wantImport {
			t.Fatalf("unexpected trusted import: %s", calls)
		}
	}
	expectedInline := 0
	if inline {
		expectedInline = 2
	}
	if strings.Count(
		string(calls),
		"--cache-to type=inline",
	) != expectedInline ||
		strings.Contains(string(calls), "--no-cache") != refresh {
		t.Fatalf("unexpected export/refresh policy: %s", calls)
	}
	fetches, _ := os.ReadFile(filepath.Join(directory, "fetches"))
	if wantImport && !strings.Contains(string(fetches),
		"/projects/3/jobs/99/artifacts/artifacts/runtime-cache.json") {
		t.Fatalf("cache lookup escaped default branch: %s", fetches)
	}
	if !wantImport && len(fetches) != 0 {
		t.Fatalf("ineligible build fetched trusted cache: %s", fetches)
	}
}

func checkTrustedRuntimeCacheReport(
	t *testing.T,
	directory string,
	trusted bool,
) {
	t.Helper()
	data, err := os.ReadFile(
		filepath.Join(directory, "artifacts/runtime-cache.json"),
	)
	if !trusted {
		if !os.IsNotExist(err) {
			t.Fatalf(
				"untrusted build exported trusted metadata: %s, %v",
				data,
				err,
			)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	want := "registry.example/project/ci@sha256:" + strings.Repeat("a", 64)
	if report["protected"] != true || report["pipeline_id"] != "42" ||
		report["runtime"] != want {
		t.Fatalf("invalid trusted cache report: %s", data)
	}
}

func TestRuntimeCacheRejectsUntrustedOrStaleMetadata(t *testing.T) {
	for _, key := range []string{"project_id", "ref", "protected", "cache_epoch", "browser", "runtime", "pipeline_id", "malformed", "unavailable", "refresh"} {
		t.Run(key, func(t *testing.T) {
			metadata := runtimeCacheMetadata()
			metadata[key] = "invalid"
			payload, _ := json.Marshal(metadata)
			env := []string{
				"CI_PIPELINE_SOURCE=push",
				"CI_COMMIT_REF_PROTECTED=true",
				"CI_COMMIT_BRANCH=main",
				"FIXTURE_CACHE_JSON=" + string(payload),
			}
			switch key {
			case "malformed":
				env = append(env, "FIXTURE_CACHE_JSON=not-json")
			case "unavailable":
				env = append(env, "FIXTURE_FETCH_FAIL=true")
			case "refresh":
				env = append(env, "CI_RUNTIME_REFRESH=true")
			}
			directory, err := runtimeImageFixture(t, env...)
			if err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(filepath.Join(directory, "calls"))
			if err != nil || strings.Contains(string(calls), "--cache-from") {
				t.Fatalf(
					"invalid cache must fall back to a build: %s, %v",
					calls,
					err,
				)
			}
			if key == "refresh" &&
				strings.Count(string(calls), "--no-cache") != 2 {
				t.Fatalf("manual refresh must rebuild both images: %s", calls)
			}
		})
	}
}

func TestRuntimeCacheVerifiesGitLabPipelineAndJob(t *testing.T) {
	for name, env := range map[string]string{
		"no-pipeline":     `FIXTURE_PIPELINES_JSON=[]`,
		"mr-pipeline":     `FIXTURE_PIPELINES_JSON=[{"id":41,"project_id":3,"ref":"main","source":"merge_request_event","status":"success"}]`,
		"other-project":   `FIXTURE_PIPELINES_JSON=[{"id":41,"project_id":4,"ref":"main","source":"push","status":"success"}]`,
		"other-branch":    `FIXTURE_PIPELINES_JSON=[{"id":41,"project_id":3,"ref":"feature","source":"push","status":"success"}]`,
		"failed-pipeline": `FIXTURE_PIPELINES_JSON=[{"id":41,"project_id":3,"ref":"main","source":"push","status":"failed"}]`,
		"tag-job":         `FIXTURE_JOBS_JSON=[{"id":99,"name":"ci-runtime","ref":"main","tag":true,"status":"success","pipeline":{"id":41}}]`,
		"other-job":       `FIXTURE_JOBS_JSON=[{"id":99,"name":"lint","ref":"main","tag":false,"status":"success","pipeline":{"id":41}}]`,
		"other-pipeline":  `FIXTURE_JOBS_JSON=[{"id":99,"name":"ci-runtime","ref":"main","tag":false,"status":"success","pipeline":{"id":40}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			payload, _ := json.Marshal(runtimeCacheMetadata())
			directory, err := runtimeImageFixture(t, env,
				"CI_PIPELINE_SOURCE=push", "CI_COMMIT_REF_PROTECTED=true",
				"CI_COMMIT_BRANCH=main", "FIXTURE_CACHE_JSON="+string(payload))
			if err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(filepath.Join(directory, "calls"))
			if err != nil || strings.Contains(string(calls), "--cache-from") {
				t.Fatalf(
					"untrusted pipeline supplied a cache: %s, %v",
					calls,
					err,
				)
			}
		})
	}
}
