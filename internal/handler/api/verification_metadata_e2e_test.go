//go:build e2e

package api_test

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/events"
	"durpdeploy/internal/testdns"
)

func TestVerificationHTTPMetadataBoundaryE2E(t *testing.T) {
	f := newArtifactE2E(t)
	path := fmt.Sprintf("/environments/%d", f.environment.ID)
	for _, host := range []string{
		"100.100.100.200", "[::ffff:100.100.100.200]",
		"[fd00:ec2::254]", "[fd00:0ec2:0:0:0:0:0:0254]",
	} {
		target := "http://" + host + "/latest/meta-data/"
		f.api(t, "PUT", "/api/v1"+path, map[string]any{
			"name": f.environment.Name, "verification_type": "http",
			"verification_target": target,
		}, 422)
		f.web(t, "PUT", path, url.Values{
			"name": {f.environment.Name}, "verification_type": {"http"},
			"verification_target": {target},
		}, 422)
	}
	for _, address := range []string{"100.100.100.200", "fd00:ec2::254"} {
		t.Run(address, func(t *testing.T) {
			checkVerificationMetadataDNS(t, f, address)
		})
	}
}

func checkVerificationMetadataDNS(
	t *testing.T,
	f *artifactE2E,
	address string,
) {
	t.Helper()
	var lookups atomic.Int32
	testdns.Install(t, func(_ string) []net.IP {
		lookups.Add(1)
		return []net.IP{net.ParseIP(address)}
	})
	configureVerification(t, f, "http", "http://metadata.invalid/latest/", 5)
	deployment := verificationDeploy(t, f,
		verificationRelease(t, f, "metadata-denied-"+address))
	f.completion(t, deployment.ID, events.DeploymentFailed)
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	logs := string(f.api(t, "GET", "/api/v1"+path+"/logs", nil, 200))
	if lookups.Load() == 0 || !strings.Contains(logs, "Verification failed") ||
		strings.Contains(
			logs,
			"HTTP status:",
		) || strings.Contains(logs, "timed out") {
		t.Fatalf("metadata DNS boundary not enforced: %s", logs)
	}
	page := f.web(t, "GET", path+"/verification", nil, 200)
	if !strings.Contains(page, "failed") {
		t.Fatalf("failed verification missing from web: %s", page)
	}
}
