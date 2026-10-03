//go:build e2e

package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/events"
)

func TestVerificationEncryptedTargetsE2E(t *testing.T) {
	for _, kind := range []string{"http", "bash"} {
		t.Run(kind, func(t *testing.T) {
			f := newVerificationE2E(t)
			target := "echo inline-check-proof"
			if kind == "http" {
				target = verificationUpstream(t, http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Query().Get("token") != "stored-query-token" {
							w.WriteHeader(403)
							return
						}
						fmt.Fprintln(w, "healthy")
					})) + "/health?token=stored-query-token"
			}
			configureVerification(t, f, kind, target, 5)
			deployment := verificationDeploy(t, f,
				verificationRelease(t, f, "encrypted-"+kind))
			f.completion(t, deployment.ID, events.DeploymentSucceeded)
			assertEncryptedVerificationSnapshot(t, f, deployment.ID, target)
		})
	}
}

func assertEncryptedVerificationSnapshot(
	t *testing.T, f *artifactE2E, deploymentID int64, target string,
) {
	t.Helper()
	env, err := f.h.repo.Queries.GetEnvironment(t.Context(), f.environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	check, err := f.h.repo.Queries.GetDeploymentVerification(
		t.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if env.VerificationTarget == target || check.Target == target {
		t.Fatal("configuration or frozen target is not encrypted")
	}
	plain, err := f.h.repo.DecryptVerificationTarget(check.Target)
	if err != nil || plain != target {
		t.Fatal("encrypted snapshot cannot recover its target")
	}
	steps, err := f.h.repo.Queries.ListDeploymentSteps(
		t.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if strings.Contains(step.ScriptBody, target) {
			t.Fatal("deployment steps contain a plaintext verification copy")
		}
	}
}
