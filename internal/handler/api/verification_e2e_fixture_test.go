//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func newVerificationE2E(t *testing.T) *artifactE2E {
	t.Helper()
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Deploy", "script_body": "printf 'deployment-step-complete\\n'",
		"interpreter": "bash", "container_image": "docker.io/library/bash:5.2",
	}, 201)
	f.api(t, "POST", f.base()+"/variables", map[string]any{
		"name": "SECRET", "value": "verification-secret-value", "secret": true,
	}, 201)
	return f
}

func verificationRelease(
	t *testing.T,
	f *artifactE2E,
	version string,
) db.Release {
	t.Helper()
	var release db.Release
	if err := json.Unmarshal(f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": version}, 201), &release); err != nil {
		t.Fatal(err)
	}
	return release
}

func verificationDeploy(
	t *testing.T,
	f *artifactE2E,
	release db.Release,
) db.Deployment {
	t.Helper()
	var deployment db.Deployment
	if err := json.Unmarshal(f.api(t, "POST", f.base()+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		}, 201), &deployment); err != nil {
		t.Fatal(err)
	}
	return deployment
}

func configureVerification(
	t *testing.T,
	f *artifactE2E,
	kind, target string,
	timeout int64,
) {
	t.Helper()
	f.api(t, "PUT", fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
		map[string]any{
			"name":                         f.environment.Name,
			"verification_type":            kind,
			"verification_target":          target,
			"verification_timeout_seconds": timeout,
		}, 200)
}

func verificationUpstream(t *testing.T, handler http.Handler) string {
	t.Helper()
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var ip net.IP
	for _, address := range addresses {
		candidate, _, err := net.ParseCIDR(address.String())
		if err == nil && candidate.To4() != nil &&
			candidate.IsGlobalUnicast() &&
			!candidate.IsLoopback() {
			ip = candidate
			break
		}
	}
	if ip == nil {
		t.Fatal("verification E2E requires a non-loopback IPv4 interface")
	}
	srv := httptest.NewUnstartedServer(handler)
	if err := srv.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	srv.Listener, err = net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

func waitVerificationStatus(
	t *testing.T,
	f *artifactE2E,
	id int64,
	want string,
) {
	t.Helper()
	waitExecutionStatus(t, f, fmt.Sprintf(
		"/api/v1/deployments/%d/status", id,
	), want)
}

func waitExecutionStatus(t *testing.T, f *artifactE2E, path, want string) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(f.api(
			t,
			"GET",
			path,
			nil,
			200,
		), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf(
				"execution %s status=%s, want=%s",
				path,
				result.Status,
				want,
			)
		case <-ticker.C:
		}
	}
}
