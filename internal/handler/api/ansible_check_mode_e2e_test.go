//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/stepdefinition"
)

func TestAnsibleCheckModeRequiresAnsibleAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	script := map[string]string{
		"name": "Script remains available", "script_body": "echo script-ran",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}
	f.api(t, "POST", f.base()+"/steps", script, 201)
	release := verificationRelease(t, f, "script-check-validation")
	f.api(t, "POST", f.base()+"/deployments", map[string]any{
		"release_id": release.ID, "environment_id": f.environment.ID,
		"ansible_check_mode": true,
	}, 422)
	f.api(t, "POST", f.base()+"/deployments", map[string]any{
		"release_id": release.ID, "environment_id": f.environment.ID,
		"ansible_check_mode": "true",
	}, 400)
	var book struct{ Runbook db.Runbook }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/runbooks", map[string]any{
			"name": "Script validation", "steps": []any{script},
		}, 201),
		&book,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/runbooks/%d/executions", f.base(), book.Runbook.ID),
		map[string]any{
			"environment_id": f.environment.ID, "ansible_check_mode": true,
		},
		422,
	)
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
}

func TestAnsibleLegacyCheckModeAPIE2E(t *testing.T) {
	f, definition, _, target := newAnsibleE2E(t)
	f.api(t, "POST", f.base()+"/steps", definition, 201)
	release := verificationRelease(t, f, "legacy-check-snapshot")
	check := true
	definition.Ansible.Check = &check
	raw, err := json.Marshal([]stepdefinition.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	// Restore a historical release and accepted run from before this migration.
	if _, err := f.h.repo.Queries.UpdateRelease(t.Context(), db.UpdateReleaseParams{
		ID: release.ID, ProjectID: f.project.ID, Version: release.Version, StepsJson: string(raw),
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := f.h.repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: f.environment.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(), "UPDATE deployments SET ansible_check_mode = NULL WHERE id = ?", legacy.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	go f.h.runner.Run(
		t.Context(),
		legacy.Deployment.ID,
		release.ID,
		f.environment.ID,
	)
	f.completion(t, legacy.Deployment.ID, events.DeploymentSucceeded)
	var rerun db.Deployment
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			fmt.Sprintf(
				"/api/v1/deployments/%d/redeploy",
				legacy.Deployment.ID,
			),
			nil,
			201,
		),
		&rerun,
	)
	f.completion(t, rerun.ID, events.DeploymentSucceeded)
	if rerun.AnsibleCheckMode.Valid {
		t.Fatal("legacy redeploy lost per-step mode")
	}
	if code, _, err := target.Exec(t.Context(), []string{"test", "!", "-e", "/tmp/native-ansible-result"}); err != nil ||
		code != 0 {
		t.Fatal("legacy check mode changed target", err)
	}
	assertAnsibleCheckDoesNotPromote(t, f, release)
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/rollback", rerun.ID),
		nil,
		409,
	)
	assertAnsibleProjectApplied(t, f, false)
	fresh := verificationDeploy(t, f, release)
	f.completion(t, fresh.ID, events.DeploymentSucceeded)
	assertAnsibleProjectApplied(t, f, true)
	if code, _, err := target.Exec(t.Context(), []string{"test", "-e", "/tmp/native-ansible-result"}); err != nil ||
		code != 0 {
		t.Fatal("fresh deployment inherited legacy check option", err)
	}
	source, err := f.h.repo.Queries.GetDeploymentStepSource(
		t.Context(),
		legacy.Deployment.ID,
	)
	if err != nil || source.StepsJson != string(raw) {
		t.Fatal("legacy snapshot changed", err)
	}
}

func assertAnsibleProjectApplied(t *testing.T, f *artifactE2E, applied bool) {
	t.Helper()
	body := f.web(t, "GET", fmt.Sprintf("/projects/%d", f.project.ID), nil, 200)
	marker := "<span data-project-environment-name>" + html.EscapeString(
		f.environment.Name,
	) + "</span>"
	_, row, found := strings.Cut(body, marker)
	if !found {
		t.Fatal("project environment panel missing")
	}
	row, _, _ = strings.Cut(row, "</tr>")
	if strings.Contains(row, "No successful deploys") == applied {
		t.Fatal("project panel misclassified legacy check or fresh apply")
	}
}
