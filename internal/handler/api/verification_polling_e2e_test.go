//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/events"
)

func TestVerificationPollingFollowsCheckStatusE2E(t *testing.T) {
	// Given: a completed deployment and a verification read from completion.
	f := newVerificationE2E(t)
	configureVerification(t, f, "bash", "echo healthy", 5)
	deployment := verificationDeploy(t, f,
		verificationRelease(t, f, "poll-status"))
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	path := fmt.Sprintf("/deployments/%d/verification", deployment.ID)
	for _, status := range []string{
		"pending", "running", "succeeded", "failed", "cancelled",
	} {
		// The fixture supplies the stale check value a concurrent read can see.
		setVerificationPollingState(t, f, deployment.ID, status)
		// When: clients read verification through its API and web endpoints.
		check := string(f.api(t, "GET", "/api/v1"+path, nil, 200))
		page := f.web(t, "GET", path, nil, 200)
		// Then: polling follows the check, even if the deployment is terminal.
		trigger := "none"
		if status == "pending" || status == "running" {
			trigger = "every 3s"
		}
		if !strings.Contains(check, `"status":"`+status+`"`) ||
			!strings.Contains(page, `hx-trigger="`+trigger+`"`) {
			t.Fatalf("status %s: API=%s web=%s", status, check, page)
		}
	}
}

func setVerificationPollingState(
	t *testing.T, f *artifactE2E, id int64, status string,
) {
	t.Helper()
	if _, err := f.h.repo.DB.ExecContext(
		t.Context(),
		"UPDATE deployment_verifications SET status = ? WHERE deployment_id = ?",
		status,
		id,
	); err != nil {
		t.Fatal(err)
	}
}
