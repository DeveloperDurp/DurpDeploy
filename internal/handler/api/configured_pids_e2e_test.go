//go:build e2e

package api_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/events"
)

const configuredPIDImage = "harbor.durp.info/dockerhub/library/python:3.12"

func TestConfiguredPIDRemoteRejectionAPIE2E(t *testing.T) {
	limits := config.Defaults().Limits.Container
	limits.PIDs, limits.HelperPIDs = 8, 8
	testConfiguredRemoteContainerRejection(
		t,
		limits,
	)
}

func testConfiguredRemoteContainerRejection(
	t *testing.T,
	limits config.ContainerLimits,
) {
	t.Helper()
	f := newConfiguredArtifactE2E(t, &limits)
	seedIncompatiblePolicyAgent(t, f)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Unsupported remote policy", "script_body": "echo must-not-run",
		"execution_target": "agent", "agent_execution_mode": "host",
	}, 201)
	release := verificationRelease(t, f, "remote-pids")
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.DeploymentFailed)
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(
		logs,
		"resource policy is incompatible",
	) {
		t.Fatalf("missing remote policy rejection: %s", logs)
	}
}

func seedIncompatiblePolicyAgent(t *testing.T, f *artifactE2E) {
	t.Helper()
	pin := strings.Repeat("a", 64)
	if _, err := f.h.repo.DB.ExecContext(t.Context(), `
INSERT INTO agents(id,name,endpoint,status,certificate_pem,certificate_fingerprint,encrypted_identity,last_heartbeat_at,agent_protocol)
VALUES('policy-agent','Policy agent','https://fixture.invalid','active','fixture',?,'fixture',?,'agent/3')`, pin, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(), `
INSERT INTO agent_pairings(agent_id,pairing_code_hash,agent_public_identity,agent_pin,server_public_identity,server_pin,encrypted_identity,state,expires_at,paired_at)
VALUES('policy-agent',?,'fixture',?,'fixture',?,'fixture','paired',0,1)`, make([]byte, 32), pin, pin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(), `
INSERT INTO agent_interpreters VALUES('policy-agent','bash');
INSERT INTO agent_execution_modes VALUES('policy-agent','host');
INSERT INTO agent_staging_capabilities VALUES('policy-agent');
INSERT INTO agent_artifact_capabilities VALUES('policy-agent');
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('policy-agent',?)`, f.environment.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredContainerPIDsAPIE2E(t *testing.T) {
	for _, limits := range [][2]int64{{8, 8}, {160, 48}} {
		t.Run(fmt.Sprint(limits[0]), func(t *testing.T) {
			testConfiguredContainerPIDBudget(t, limits)
		})
	}
}

func testConfiguredContainerPIDBudget(t *testing.T, limits [2]int64) {
	t.Helper()
	container := config.Defaults().Limits.Container
	container.PIDs, container.HelperPIDs = limits[0], limits[1]
	f := newConfiguredArtifactE2E(t, &container)
	if stagingRuntime(
		t,
		"images",
		"--quiet",
		configuredPIDImage,
	) == "" {
		stagingRuntime(t, "pull", "--quiet", configuredPIDImage)
	}
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "PID budget", "interpreter": "python3",
		"container_image": configuredPIDImage,
		"script_body":     fmt.Sprintf(pidBudgetScript, limits[0]),
	}, 201)
	release := verificationRelease(t, f, "configured-pids")
	deployment := verificationDeploy(t, f, release)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	waitVerificationLog(t, f, path, "pid-budget-ready")
	ids := strings.Fields(stagingRuntime(t, "ps", "--quiet",
		"--filter=label=io.durpdeploy.namespace="+
			os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")+":"+
			os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")))
	if len(ids) != 2 {
		t.Fatalf("expected workload and staging helper: %v", ids)
	}
	inspectConfiguredPIDContainers(t, ids, limits)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	logs := string(f.api(t, "GET", path+"/logs", nil, 200))
	for _, marker := range []string{"process-budget-confirmed", "thread-budget-confirmed", "pid-budget-complete"} {
		if !strings.Contains(logs, marker) {
			t.Fatalf("missing %s: %s", marker, logs)
		}
	}
	if remaining := strings.TrimSpace(stagingRuntime(t, "ps", "--all", "--quiet",
		"--filter=label=io.durpdeploy.namespace="+
			os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")+":"+
			os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE"))); remaining != "" {
		t.Fatalf(
			"containers remain after confirmed completion: %s",
			remaining,
		)
	}
}

func inspectConfiguredPIDContainers(
	t *testing.T,
	ids []string,
	limits [2]int64,
) {
	t.Helper()
	for _, id := range ids {
		name := stagingRuntime(t, "inspect", "--format={{.Name}}", id)
		helper := strings.Contains(name, "-keeper")
		want := limits[0]
		if helper {
			want = limits[1]
		}
		got := strings.TrimSpace(stagingRuntime(t, "inspect",
			"--format={{.HostConfig.PidsLimit}}", id))
		if got != strconv.FormatInt(want, 10) {
			t.Fatalf("%s PID limit=%s want=%d", name, got, want)
		}
		if !helper {
			stagingRuntime(t, "exec", id, "python3", "-c",
				"open('/tmp/finish', 'w').close()")
		}
	}
}

const pidBudgetScript = `import errno, os, pathlib, threading, time
limit = %d
root = pathlib.Path('/sys/fs/cgroup')
if not (root / 'pids.max').exists():
    root = root / 'pids'
assert int((root / 'pids.max').read_text()) == limit
initial = int((root / 'pids.current').read_text())
reader, writer = os.pipe()
children = []
try:
    for _ in range(limit + 1):
        try:
            child = os.fork()
        except OSError as error:
            assert error.errno == errno.EAGAIN, error
            break
        if child == 0:
            os.close(writer)
            os.read(reader, 1)
            os._exit(0)
        children.append(child)
    else:
        raise AssertionError('process creation exceeded PID budget')
    assert len(children) == limit - initial, (len(children), initial, limit)
finally:
    os.close(writer)
    os.close(reader)
    for child in children:
        os.waitpid(child, 0)
print('process-budget-confirmed', len(children), flush=True)
release = threading.Event()
threads = []
try:
    for _ in range(limit + 1):
        thread = threading.Thread(target=release.wait)
        try:
            thread.start()
        except RuntimeError:
            break
        threads.append(thread)
    else:
        raise AssertionError('thread creation exceeded PID budget')
    assert len(threads) == limit - initial, (len(threads), initial, limit)
finally:
    release.set()
    for thread in threads:
        thread.join()
print('thread-budget-confirmed', len(threads), flush=True)
print('pid-budget-ready', flush=True)
while not pathlib.Path('/tmp/finish').exists():
    time.sleep(0.05)
print('pid-budget-complete', flush=True)
`
