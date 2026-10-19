//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
)

func checkAuditAmbiguousTargets(t *testing.T) {
	t.Helper()
	f := newArtifactE2E(t, fakeDocker(t))
	project := seedProject(t, f.h.repo)
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	for i := range 2 {
		f.api(t, "POST", f.base()+"/releases",
			map[string]string{"version": fmt.Sprint(i)}, 201)
		seedDeployment(t, f.h.repo, 1, f.environment.ID, "succeeded")
	}
	for i := range 2 {
		createAuditTargetRunbook(t, f, f.base(), fmt.Sprint(i))
	}
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", base+"/releases",
		map[string]string{"version": "target"}, 201), &release)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", base, release.ID),
		nil,
		200,
	)
	var deployed db.Deployment
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		base+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		201,
	), &deployed)
	f.completion(t, deployed.ID, events.DeploymentSucceeded)
	book := createAuditTargetRunbook(t, f, base, "target")
	var execution db.RunbookExecution
	decodeStepLogTest(t, f.api(t, "POST",
		fmt.Sprintf("%s/runbooks/%d/executions", base, book.ID),
		map[string]int64{"environment_id": f.environment.ID}, 201), &execution)
	f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
	if release.ID == project.ID || deployed.ID == project.ID ||
		book.ID == project.ID {
		t.Fatal(
			"fixture must distinguish target IDs from enclosing project IDs",
		)
	}
	// The same numeric IDs name unrelated records in the first project.
	for _, action := range []string{"create_release", "refresh_release",
		"create_deployment", "create_runbook", "execute_runbook"} {
		t.Run(action, func(t *testing.T) {
			checkAmbiguousAuditAction(t, f, action)
		})
	}
}

func createAuditTargetRunbook(
	t *testing.T, f *artifactE2E, base, name string,
) db.Runbook {
	t.Helper()
	var saved struct{ Runbook db.Runbook }
	decodeStepLogTest(t, f.api(t, "POST", base+"/runbooks", map[string]any{
		"name": name,
		"steps": []map[string]string{{"name": "noop", "script_body": "true",
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2"}},
	}, 201), &saved)
	return saved.Runbook
}

func checkAmbiguousAuditAction(t *testing.T, f *artifactE2E, action string) {
	t.Helper()
	var result struct{ Items []repository.AuditLogEntry }
	decodeStepLogTest(t, f.api(t, "GET", "/api/v1/admin/audit?action="+action,
		nil, 200), &result)
	if len(result.Items) == 0 {
		t.Fatal("expected actual HTTP operation audit entries")
	}
	for _, entry := range result.Items {
		if entry.TargetURL != "" {
			t.Errorf("ambiguous %s linked unrelated record: %+v", action, entry)
		}
	}
	page := f.web(t, "GET", "/admin/audit?action="+action, nil, 200)
	if strings.Contains(string(page), "data-audit-target") {
		t.Fatal("web links an ambiguous recorded target")
	}
}
