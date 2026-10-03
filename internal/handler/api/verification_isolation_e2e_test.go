//go:build e2e

package api_test

import (
	"encoding/base64"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/verification"
	"github.com/google/uuid"
)

func TestVerificationProjectImageIsolationE2E(t *testing.T) {
	// Given: a deployer selects an image whose Bash copies stdin into logs.
	f := newVerificationE2E(t)
	image := verificationAttackImage(t)
	deployer := seedAPIUser(
		t,
		f.h.repo,
		"image-deployer@example.test",
		"deployer",
	)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: deployer.ID, Role: "deployer",
		}); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"BASH_ENV": "/tmp/untrusted-startup", "LD_PRELOAD": "/tmp/untrusted-loader",
	} {
		f.api(t, "POST", f.base()+"/variables", map[string]any{
			"name": name, "value": value,
		}, 201)
	}
	const target = `private_value=inline-private-proof
test "${BASH_ENV-unset}" = unset && test "${LD_PRELOAD-unset}" = unset || exit 1
echo verification-isolated`
	configureVerification(t, f, "bash", target, 10)
	_, f.token = seedAPIToken(t, f.h.repo, deployer.ID)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name":            "Untrusted final step",
		"script_body":     "echo project-image-control",
		"interpreter":     "bash",
		"container_image": image,
	}, 201)
	release := verificationRelease(t, f, "isolated-v1")
	// When: the deployer runs the release with private environment verification.
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	// Then: the image runs ordinary steps, but never receives the private script.
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	logs := string(f.api(t, "GET", "/api/v1"+path+"/logs", nil, 200))
	page := html.UnescapeString(f.web(t, "GET", path, nil, 200))
	encoded := base64.StdEncoding.EncodeToString([]byte(target))
	for _, output := range []string{logs, page} {
		if strings.Contains(output, encoded) ||
			strings.Contains(output, "inline-private-proof") ||
			!strings.Contains(output, "verification-isolated") ||
			!strings.Contains(output, "project-image-ran") {
			t.Fatal("verification isolation or ordinary image execution failed")
		}
	}
}

func verificationAttackImage(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	image := "localhost/verification-attack-" + uuid.NewString()
	dockerfile := "FROM " + verification.BashImage + `
COPY --chmod=755 bash /usr/local/bin/bash
`
	script := `#!/bin/sh
printf 'project-image-ran\n'
base64 | tr -d '\n'
printf '\n'
`
	for name, contents := range map[string]string{
		"Dockerfile": dockerfile, "bash": script,
	} {
		if err := os.WriteFile(filepath.Join(directory, name),
			[]byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	kind := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	if output, err := exec.CommandContext(
		t.Context(),
		kind,
		"build",
		"--quiet",
		"--tag",
		image,
		directory,
	).CombinedOutput(); err != nil {
		t.Fatalf("build malicious test image: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command(kind, "image", "rm", image).
			CombinedOutput(); err != nil {
			t.Errorf("remove malicious test image: %v: %s", err, output)
		}
	})
	return image
}
