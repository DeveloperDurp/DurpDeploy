//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/runner"
	"github.com/google/uuid"
)

func TestArtifactGatePollingReadOnlyE2E(t *testing.T) {
	f, deployment, _ := newGateDeployment(t)
	f.h.repo.DB.SetMaxOpenConns(1)
	if _, err := f.h.repo.DB.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.h.repo.DB.Exec("PRAGMA query_only=OFF"); err != nil {
			t.Error(err)
		}
	}()
	// API and web polling work even when the database forbids writes.
	f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200)
	f.web(t, "GET", fmt.Sprintf("/deployments/%d/artifact-gates",
		deployment.ID), nil, 200)
}

func TestArtifactGateImagePruneRetentionE2E(t *testing.T) {
	f := newArtifactE2E(t)
	identity := uuid.NewString()
	image := "localhost/durpdeploy-gate-retention:" + identity
	label := "io.durpdeploy.prune-test=" + identity
	directory := t.TempDir()
	dockerfile := "FROM docker.io/library/bash:5.2\n"
	if os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") == "podman" {
		// Docker deliberately rejects VOLUME images for executable steps.
		dockerfile += "VOLUME /image-data\n"
	}
	if err := os.WriteFile(filepath.Join(directory, "Dockerfile"),
		[]byte(dockerfile),
		0600); err != nil {
		t.Fatal(err)
	}
	stagingRuntime(t, "build", "--tag", image, "--label", label, directory)
	// Remove only this fixture's holders and image, including on assertion failure.
	t.Cleanup(func() {
		ids := stagingRuntime(t, "ps", "--all", "--quiet",
			"--filter=label=io.durpdeploy.namespace="+
				os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")+":"+
				os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")+":gate-images",
			"--filter=ancestor="+image)
		if ids != "" {
			stagingRuntime(t, append([]string{"rm", "--force", "--volumes"},
				strings.Fields(ids)...)...)
		}
		stagingRuntime(t, "image", "prune", "--all", "--force",
			"--filter=label="+label)
	})
	for _, name := range []string{"Generate", "Apply"} {
		step := map[string]any{
			"name":            name,
			"container_image": image,
			"script_body":     `test "$(cat "$DURPDEPLOY_APPROVED_DIR/plan")" = pinned`,
		}
		if name == "Generate" {
			step["approval_artifact_path"] = "plan"
			step["approval_review_path"] = "review.json"
			step["approval_review_format"] = "summary"
			step["script_body"] = `printf pinned > "$DURPDEPLOY_STAGE_DIR/plan"
printf '{"create":1}' > "$DURPDEPLOY_STAGE_DIR/review.json"`
		}
		f.api(t, "POST", f.base()+"/steps", step, 201)
	}
	release := verificationRelease(t, f, "retained-image")
	deployment := verificationDeploy(t, f, release)
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	scope := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") + ":" +
		os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE") + ":gate-images"
	holders := stagingRuntime(t, "ps", "--all", "--quiet",
		"--filter=label=io.durpdeploy.namespace="+scope)
	if len(strings.Fields(holders)) != 2 {
		t.Fatalf("expected two image references: %s", holders)
	}
	var imageVolumes []string
	for _, id := range strings.Fields(holders) {
		var mounts []struct{ Name string }
		if err := json.Unmarshal([]byte(stagingRuntime(t, "inspect",
			"--format={{json .Mounts}}", id)), &mounts); err != nil {
			t.Fatal(err)
		}
		for _, mount := range mounts {
			if mount.Name != "" {
				imageVolumes = append(imageVolumes, mount.Name)
			}
		}
	}
	pinned, err := f.h.repo.Queries.GetArtifactGateImage(t.Context(),
		db.GetArtifactGateImageParams{
			DeploymentID: deployment.ID, StepIndex: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	// Startup reconciliation must preserve the waiting deployment's references.
	namespace := os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", namespace+"-gate-images")
	neighbor := runner.New(f.h.repo, runner.NewLogBroker())
	if !neighbor.ContainerRuntimeReady() {
		t.Fatal("neighbor namespace startup failed")
	}
	t.Cleanup(neighbor.KillAll)
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", namespace)
	restarted := runner.New(f.h.repo, runner.NewLogBroker())
	if !restarted.ContainerRuntimeReady() {
		t.Fatal("startup image reconciliation failed")
	}
	t.Cleanup(restarted.KillAll)
	stagingRuntime(t, "image", "prune", "--all", "--force",
		"--filter=label="+label)
	if actual := stagingRuntime(t, "image", "inspect", "--format={{.Id}}",
		image); actual != pinned {
		t.Fatalf("pruning replaced pinned image: %s != %s", actual, pinned)
	}
	gate, err := f.h.repo.Queries.GetArtifactGate(t.Context(),
		db.GetArtifactGateParams{DeploymentID: deployment.ID, StepIndex: 0})
	if err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", gateAPIPath(deployment.ID)+"/0/approve",
		map[string]any{"revision": gate.Revision,
			"sha256": gate.ArtifactSha256}, 200)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	if err := restarted.CleanupArtifactGateImages(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ids := stagingRuntime(t, "ps", "--all", "--quiet",
		"--filter=ancestor="+image); ids != "" {
		t.Fatalf("terminal deployment retained image holders: %s", ids)
	}
	volumes := strings.Fields(stagingRuntime(t, "volume", "ls", "--quiet"))
	for _, expectedRemoved := range imageVolumes {
		for _, remaining := range volumes {
			if expectedRemoved == remaining {
				t.Fatalf("image reference leaked volume %s", remaining)
			}
		}
	}
}
