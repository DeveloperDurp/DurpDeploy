//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func queuedCancellationFixture(
	t *testing.T,
) (*artifactE2E, db.Deployment, db.Deployment) {
	t.Helper()
	f := newArtifactE2E(t)
	for range 2 {
		f.api(t, "POST", f.base()+"/steps", map[string]string{
			"name": "Duplicate queued step", "script_body": "echo must-not-run",
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		}, 201)
	}
	release := verificationRelease(t, f, "queued-cancellation")
	// Reserve the lane without launching execution. The public request must queue.
	owner, err := f.h.repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: f.environment.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	queued := verificationDeploy(t, f, release)
	if queued.Status != "queued" {
		t.Fatalf("deployment status=%s, want queued", queued.Status)
	}
	return f, owner.Deployment, queued
}

func assertQueuedCancellation(
	t *testing.T,
	f *artifactE2E,
	owner, queued db.Deployment,
) {
	t.Helper()
	path := fmt.Sprintf("/api/v1/deployments/%d", queued.ID)
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(t, "GET", path, nil, 200), &deployment)
	if deployment.Status != "cancelled" || deployment.StartedAt.Valid {
		t.Fatalf("queued cancellation=%+v", deployment)
	}
	var logs []db.DeploymentLog
	decodeStepLogTest(t, f.api(t, "GET", path+"/logs", nil, 200), &logs)
	assertQueuedCancellationRecords(t, logs)
	stream := f.readStepLogs(t, path+"/logs/stream?format=structured", "")
	if len(stream) != 2 || stream[0].State != "cancelled" ||
		stream[1].State != "cancelled" || stream[0].Line != logs[1].Line {
		t.Fatalf("queued cancellation stream=%+v", stream)
	}
	page := f.web(t, "GET", fmt.Sprintf("/deployments/%d", queued.ID), nil, 200)
	if strings.Count(page, ">Not run: cancelled while queued<") != 2 ||
		strings.Count(page, "No output yet.") > 1 ||
		strings.Contains(page, "State unavailable") {
		t.Fatal("reloaded page omitted queued cancellation reason")
	}
	decodeStepLogTest(t, f.api(t, "GET", fmt.Sprintf(
		"/api/v1/deployments/%d", owner.ID), nil, 200), &deployment)
	if deployment.Status != "pending" {
		t.Fatal("queued cancellation changed the lane owner")
	}
}

func assertQueuedCancellationRecords(t *testing.T, logs []db.DeploymentLog) {
	t.Helper()
	if len(logs) != 2 {
		t.Fatalf("queued cancellation records=%+v", logs)
	}
	seen := map[int64]bool{}
	for _, log := range logs {
		if !log.StepIndex.Valid || log.StepState.String != "cancelled" ||
			!strings.Contains(log.Line, "Not run: cancelled while queued") ||
			!strings.Contains(
				log.Line,
				"No output exists because this step never started.",
			) ||
			strings.Contains(log.Line, "must-not-run") {
			t.Fatalf("queued lifecycle record=%+v", log)
		}
		seen[log.StepIndex.Int64] = true
	}
	if !seen[0] || !seen[1] {
		t.Fatal("duplicate step names lost snapshot positions")
	}
}

func TestQueuedCancellationAPIWebE2E(t *testing.T) {
	for _, surface := range []string{"api", "web"} {
		t.Run(surface, func(t *testing.T) {
			f, owner, queued := queuedCancellationFixture(t)
			path := fmt.Sprintf("/deployments/%d/cancel", queued.ID)
			if surface == "api" {
				f.api(t, "POST", "/api/v1"+path, nil, 200)
			} else {
				f.web(t, "POST", path, nil, 303)
			}
			assertQueuedCancellation(t, f, owner, queued)
		})
	}
}
