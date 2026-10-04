//go:build e2e

package api_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/events"
	"durpdeploy/internal/verification"
)

func TestVerificationHTTPTransportTimeoutPreservesDeadline(t *testing.T) {
	for _, phase := range []string{"headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			upstream := verificationUpstream(t,
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if phase == "body" {
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
				}))
			// The client deadline expires while the caller's context stays live.
			err := verification.CheckHTTP(t.Context(), verification.Settings{
				Target: upstream, TimeoutSeconds: 1,
			}, io.Discard)
			if !errors.Is(err, context.DeadlineExceeded) ||
				t.Context().Err() != nil {
				t.Fatalf("%s timeout lost its deadline: %v", phase, err)
			}
		})
	}
}

func TestVerificationAPIWebContainerE2E(t *testing.T) {
	// Given: a real server, encrypted release variables, and local containers.
	f := newVerificationE2E(t)
	release := verificationRelease(t, f, "verified-v1")
	configureVerification(
		t,
		f,
		"bash",
		`printf '%s\n' "$SECRET"; test "$SECRET" = verification-secret-value`,
		5,
	)

	// When: the API starts a deployment with Bash verification.
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)

	// Then: verification gates success and both surfaces expose redacted output.
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	check := string(f.api(t, "GET", "/api/v1"+path+"/verification", nil, 200))
	if !strings.Contains(check, `"status":"succeeded"`) ||
		strings.Contains(check, "verification-secret-value") {
		t.Fatalf("verification metadata=%s", check)
	}
	logs := string(f.api(t, "GET", "/api/v1"+path+"/logs", nil, 200))
	if strings.Contains(logs, "verification-secret-value") ||
		!strings.Contains(logs, "Verification succeeded") ||
		!strings.Contains(logs, "[REDACTED]") {
		t.Fatalf("verification logs=%s", logs)
	}
	page := f.web(t, "GET", path, nil, 200)
	if !strings.Contains(page, "Verification succeeded") ||
		strings.Contains(page, "verification-secret-value") {
		t.Fatalf("verification web output was absent or unredacted")
	}
	status := f.web(t, "GET", path+"/verification", nil, 200)
	if !strings.Contains(status, "succeeded") {
		t.Fatalf("web verification status=%s", status)
	}
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", f.base(), release.ID),
		nil,
		409,
	)
}

func TestVerificationHTTPFailureAndTimeoutE2E(t *testing.T) {
	for _, scenario := range []string{"failure", "timeout", "redirect", "success"} {
		t.Run(scenario, func(t *testing.T) {
			checkHTTPVerificationScenario(t, scenario)
		})
	}
}

func checkHTTPVerificationScenario(t *testing.T, scenario string) {
	t.Helper()
	// Given: a service responds unsuccessfully, stalls, redirects, or succeeds.
	f := newVerificationE2E(t)
	upstream := verificationUpstream(
		t,
		verificationHTTPResponse(scenario),
	)
	configureVerification(t, f, "http", upstream, 1)
	release := verificationRelease(t, f, "http-v1")

	// When: HTTP verification runs after the deployment steps.
	deployment := verificationDeploy(t, f, release)
	wantEvent, wantStatus := events.DeploymentFailed, "failed"
	if scenario == "success" {
		wantEvent, wantStatus = events.DeploymentSucceeded, "succeeded"
	}
	f.completion(t, deployment.ID, wantEvent)

	// Then: status, logs, audit, notification history, and web agree.
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	waitVerificationStatus(t, f, deployment.ID, wantStatus)
	check := string(
		f.api(t, "GET", "/api/v1"+path+"/verification", nil, 200),
	)
	if !strings.Contains(check, `"status":"`+wantStatus+`"`) {
		t.Fatalf("verification=%s", check)
	}
	logs := string(f.api(t, "GET", "/api/v1"+path+"/logs", nil, 200))
	if strings.Contains(logs, "verification-secret-value") ||
		!strings.Contains(logs, "Verification "+wantStatus) {
		t.Fatalf("verification logs=%s", logs)
	}
	if scenario == "timeout" && !strings.Contains(logs, "timed out") {
		t.Fatalf("missing timeout reason: %s", logs)
	}
	audit := string(
		f.api(
			t,
			"GET",
			"/api/v1/admin/audit?action=verification_"+wantStatus,
			nil,
			200,
		),
	)
	if !strings.Contains(audit, "verification_"+wantStatus) {
		t.Fatalf("verification audit=%s", audit)
	}
	notifications := string(
		f.api(t, "GET", "/api/v1/admin/notifications", nil, 200),
	)
	if !strings.Contains(notifications, string(wantEvent)) {
		t.Fatalf("verification notification history=%s", notifications)
	}
	f.web(t, "GET", path+"/verification", nil, 200)
}

func verificationHTTPResponse(scenario string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch scenario {
		case "failure":
			w.WriteHeader(503)
		case "timeout":
			<-r.Context().Done()
			return
		case "redirect":
			http.Redirect(w, r, "http://127.0.0.1/private", 302)
		}
		fmt.Fprintln(w, "verification-secret-value")
	})
}

func TestVerificationCancellationAndSnapshotE2E(t *testing.T) {
	// Given: a verification service blocks until the client cancels.
	f := newVerificationE2E(t)
	started := make(chan struct{}, 1)
	upstream := verificationUpstream(
		t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started <- struct{}{}
			<-r.Context().Done()
		}),
	)
	configureVerification(t, f, "http", upstream, 30)
	release := verificationRelease(t, f, "cancel-v1")
	deployment := verificationDeploy(t, f, release)
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("verification did not start")
	}
	configureVerification(t, f, "", "", 30)

	// When: an operator cancels through the web route.
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	f.web(t, "POST", path+"/cancel", url.Values{}, 303)
	waitVerificationStatus(t, f, deployment.ID, "cancelled")

	// Then: the frozen check remains HTTP and neither check nor deployment succeeds.
	check := string(f.api(t, "GET", "/api/v1"+path+"/verification", nil, 200))
	if !strings.Contains(check, `"type":"http"`) ||
		!strings.Contains(check, `"status":"cancelled"`) {
		t.Fatalf("cancelled verification=%s", check)
	}
}

func TestVerificationBashCancellationAndTimeoutE2E(t *testing.T) {
	for _, scenario := range []string{"cancel", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			checkBashCancellationAndTimeout(t, scenario)
		})
	}
}

func checkBashCancellationAndTimeout(t *testing.T, scenario string) {
	t.Helper()
	// Given: Bash verification blocks after ordinary deployment succeeds.
	f := newVerificationE2E(t)
	timeout := int64(30)
	if scenario == "timeout" {
		timeout = 1
	}
	configureVerification(
		t, f, "bash", "echo verification-blocking; sleep 60", timeout,
	)
	deployment := verificationDeploy(
		t, f, verificationRelease(t, f, "bash-"+scenario),
	)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	if scenario == "cancel" {
		waitVerificationLog(t, f, path, "verification-blocking")
		// When: an operator cancels the check through the API.
		f.api(t, "POST", path+"/cancel", nil, 200)
		waitVerificationStatus(t, f, deployment.ID, "cancelled")
		check := string(f.api(t, "GET", path+"/verification", nil, 200))
		if !strings.Contains(check, `"status":"cancelled"`) {
			t.Fatalf("check=%s", check)
		}
	} else {
		// Then: the deadline fails verification and the deployment.
		f.completion(t, deployment.ID, events.DeploymentFailed)
		waitVerificationStatus(t, f, deployment.ID, "failed")
		logs := string(f.api(t, "GET", path+"/logs", nil, 200))
		if !strings.Contains(logs, "timed out") {
			t.Fatalf("timeout logs=%s", logs)
		}
	}
}

func waitVerificationLog(t *testing.T, f *artifactE2E, path, text string) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		logs := string(f.api(t, "GET", path+"/logs", nil, 200))
		if strings.Contains(logs, text) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("verification output did not appear: %q", text)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
