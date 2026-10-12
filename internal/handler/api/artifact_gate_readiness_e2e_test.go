//go:build e2e

package api_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/views/pages"
)

// publishingGate holds the real runtime's first staging-volume cleanup.
// Generation and capture run normally; no production timing hooks are needed.
func publishingGate(t *testing.T) (*artifactE2E, db.Deployment, func()) {
	t.Helper()
	kind := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	if kind == "" {
		kind = "docker"
		if _, err := exec.LookPath("podman"); err == nil {
			kind = "podman"
		}
		t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", kind)
	}
	binary, err := exec.LookPath(kind)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{
		IP: net.IPv4(127, 0, 0, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	dir := t.TempDir()
	armed := filepath.Join(dir, "armed")
	if err := os.WriteFile(armed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_GATE_CLEANUP_BINARY", binary)
	t.Setenv("TEST_GATE_CLEANUP_ADDRESS", listener.Addr().String())
	t.Setenv("TEST_GATE_CLEANUP_ARMED", armed)
	wrapper := filepath.Join(dir, "runtime")
	if err := os.WriteFile(wrapper, []byte(`#!/bin/bash
set -eu
previous=''
for argument in "$@"; do
  if [[ "$previous" == volume && "$argument" == rm && -f "$TEST_GATE_CLEANUP_ARMED" ]]; then
    rm "$TEST_GATE_CLEANUP_ARMED"
    address="$TEST_GATE_CLEANUP_ADDRESS"
    exec 3<>"/dev/tcp/${address%:*}/${address##*:}"
    printf 'cleanup\n' >&3
    read -r release <&3
    exec 3>&-
  fi
  previous="$argument"
done
exec "$TEST_GATE_CLEANUP_BINARY" "$@"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	f := newArtifactE2E(t, wrapper)
	for _, step := range []map[string]any{
		{
			"name": "Plan", "container_image": "docker.io/library/bash:5.2",
			"approval_artifact_path": "tfplan",
			"approval_review_path":   "review.json", "approval_review_format": "terraform",
			"script_body": `printf plan > "$DURPDEPLOY_STAGE_DIR/tfplan"
printf '%s' '{"format_version":"1.2","resource_changes":[]}' > "$DURPDEPLOY_STAGE_DIR/review.json"`,
		},
		{
			"name": "Apply", "container_image": "docker.io/library/bash:5.2",
			"script_body": `test "$(cat "$DURPDEPLOY_APPROVED_DIR/tfplan")" = plan`,
		},
	} {
		f.api(t, "POST", f.base()+"/steps", step, 201)
	}
	deployment := verificationDeploy(t, f, verificationRelease(t, f, "ready"))
	if err := listener.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	connection, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if _, err := io.WriteString(connection, "continue\n"); err != nil {
				t.Error(err)
			}
			if err := connection.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(release)
	if err := connection.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if signal, err := bufio.NewReader(connection).ReadString('\n'); err != nil ||
		signal != "cleanup\n" {
		t.Fatalf("cleanup signal=%q err=%v", signal, err)
	}
	return f, deployment, release
}

func TestArtifactGateReadinessDuringCleanupE2E(t *testing.T) {
	// Given: a saved plan whose actual staging cleanup is still blocked.
	f, deployment, release := publishingGate(t)
	path := gateAPIPath(deployment.ID)
	var state db.Deployment
	if err := json.Unmarshal(f.api(t, "GET", fmt.Sprintf(
		"/api/v1/deployments/%d", deployment.ID,
	), nil, 200), &state); err != nil || state.Status != "publishing_artifact" {
		t.Fatalf("publication state=%+v err=%v", state, err)
	}
	var gates []pages.ArtifactGateInfo
	if err := json.Unmarshal(f.api(t, "GET", path, nil, 200), &gates); err != nil ||
		len(gates) != 1 {
		t.Fatalf("published gates=%+v err=%v", gates, err)
	}
	var publishing []struct {
		DecisionReady *bool `json:"decision_ready"`
	}
	if err := json.Unmarshal(f.api(t, "GET", path, nil, 200), &publishing); err != nil ||
		len(publishing) != 1 ||
		publishing[0].DecisionReady == nil ||
		*publishing[0].DecisionReady {
		t.Fatalf("publishing readiness=%+v err=%v", publishing, err)
	}
	identity := map[string]any{
		"revision": gates[0].Revision,
		"sha256":   gates[0].SHA256,
	}
	// When: the API and web are used before cleanup has completed.
	for _, action := range []string{"approve", "reject"} {
		f.api(t, "POST", path+"/0/"+action, identity, 409)
	}
	panel := f.web(t, "GET", fmt.Sprintf(
		"/deployments/%d/artifact-gates", deployment.ID,
	), nil, 200)
	// Then: an unavailable decision is not offered, while polling continues.
	if strings.Contains(panel, "/approve\"") ||
		strings.Contains(panel, "/reject\"") {
		t.Fatal(
			"approval controls appeared while deployment was publishing_artifact",
		)
	}
	if !strings.Contains(panel, "every 3s") {
		t.Fatal("publication panel stopped polling before readiness")
	}
	release()
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	var ready []struct {
		DecisionReady bool `json:"decision_ready"`
	}
	if err := json.Unmarshal(f.api(t, "GET", path, nil, 200), &ready); err != nil ||
		len(ready) != 1 ||
		!ready[0].DecisionReady {
		t.Fatalf("ready gates=%+v err=%v", ready, err)
	}
	f.api(t, "POST", path+"/0/approve", identity, 200)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
}

func TestArtifactGateWebDecisionRecoveryE2E(t *testing.T) {
	// Given: a current gate with a stale submitted revision.
	f, deployment, gate := newGateDeployment(t)
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	// When: either web decision submits that stale identity.
	for _, action := range []string{"approve", "reject"} {
		form := url.Values{
			"revision": {"99"}, "sha256": {gate.SHA256},
			"csrf_token": {f.csrf},
		}
		request, err := http.NewRequestWithContext(t.Context(), "POST",
			f.baseURL+path+"/artifact-gates/0/"+action,
			strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := f.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
		// Then: navigation returns to the deployment with a warning.
		if response.StatusCode != 303 ||
			response.Header.Get(
				"Location",
			) != path+"?artifact_decision=conflict" {
			t.Fatalf(
				"decision response=%d location=%s",
				response.StatusCode,
				response.Header.Get("Location"),
			)
		}
	}
	var count int
	if err := f.h.repo.DB.QueryRow(
		"SELECT COUNT(*) FROM audit_log WHERE action IN ('approve_artifact', 'reject_artifact')",
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed decisions audited: count=%d err=%v", count, err)
	}
	page := f.web(t, "GET", path+"?artifact_decision=conflict", nil, 200)
	if !strings.Contains(page, "data-artifact-decision-warning") {
		t.Fatal("recovered deployment omitted decision warning")
	}
}
