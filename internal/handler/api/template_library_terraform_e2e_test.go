//go:build e2e

package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
	"github.com/google/uuid"
)

func TestTemplateLibraryTerraformE2E(t *testing.T) {
	for _, decision := range []string{"approve", "reject", "stale", "existing-context"} {
		t.Run(
			decision,
			func(t *testing.T) { checkLibraryTerraform(t, decision) },
		)
	}
}

type libraryTerraformState struct {
	mutex  sync.Mutex
	state  []byte
	writes int
	locks  int
}

func (backend *libraryTerraformState) serve(
	w http.ResponseWriter,
	request *http.Request,
) {
	backend.mutex.Lock()
	defer backend.mutex.Unlock()
	switch request.Method {
	case "GET":
		if len(backend.state) == 0 {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(backend.state)
	case "POST":
		data, err := io.ReadAll(request.Body)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		backend.state = data
		backend.writes++
	case "LOCK":
		backend.locks++
	case "UNLOCK":
	default:
		w.WriteHeader(405)
	}
}

func checkLibraryTerraform(t *testing.T, decision string) {
	t.Helper()
	f := newArtifactE2E(t)
	f.completionTimeout = 2 * time.Minute
	backend := &libraryTerraformState{}
	address := verificationUpstream(t, http.HandlerFunc(backend.serve))
	if os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") == "podman" {
		parsed, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		address = "http://host.containers.internal:" + parsed.Port()
	}
	image := libraryTerraformImage(t, f, address)
	if decision == "existing-context" {
		f.api(t, "POST", f.base()+"/steps", map[string]any{
			"name": "Existing context", "container_image": image,
			"script_body": "mkdir \"$DURPDEPLOY_STAGE_DIR/terraform-context\"\nprintf old-plan > \"$DURPDEPLOY_STAGE_DIR/terraform-context/tfplan\"\nprintf '{}' > \"$DURPDEPLOY_STAGE_DIR/terraform-context/review.json\"",
		}, 201)
	}
	var entry repository.LibraryTemplate
	decodeStepLogTest(
		t,
		f.api(
			t,
			"GET",
			"/api/v1/template-library/terraform-plan-apply",
			nil,
			200,
		),
		&entry,
	)
	input := repository.ApplyLibraryTemplate{
		Version: entry.Version, Reviewed: true,
		VariableValues: map[string]string{
			"TF_CONFIG_DIR": "/opt/library-terraform", "TF_CONTAINER_IMAGE": image,
			"TF_HTTP_PASSWORD": "fixture-backend-secret",
		},
	}
	f.api(
		t,
		"POST",
		f.base()+"/template-library/terraform-plan-apply/apply",
		input,
		201,
	)
	deployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "terraform-library"),
	)
	libraryRuntimeDiagnostics(t, f, deployment.ID)
	if decision == "existing-context" {
		checkLibraryTerraformContextRejected(t, f, backend, deployment.ID)
		return
	}
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	var gates []pages.ArtifactGateInfo
	decodeStepLogTest(
		t,
		f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
		&gates,
	)
	if len(gates) != 1 || gates[0].Review.Create != 1 {
		t.Fatal("missing saved-plan review", gates)
	}
	backend.mutex.Lock()
	before := backend.writes
	if decision == "stale" {
		backend.state = []byte(
			`{"version":4,"terraform_version":"1.13.5","serial":1,"lineage":"ad93c268-710a-49a0-841b-02ce7a28e124","outputs":{},"resources":[]}`,
		)
	}
	backend.mutex.Unlock()
	if before != 0 {
		t.Fatal("template applied infrastructure before approval")
	}
	action := "approve"
	if decision == "reject" {
		action = "reject"
	}
	f.api(t, "POST", gateAPIPath(deployment.ID)+"/0/"+action, map[string]any{
		"sha256": gates[0].SHA256, "revision": 1,
	}, 200)
	want := events.DeploymentSucceeded
	if decision == "stale" {
		want = events.DeploymentFailed
	}
	if decision == "reject" {
		waitVerificationStatus(t, f, deployment.ID, "rejected")
	} else {
		f.completion(t, deployment.ID, want)
	}
	checkLibraryTerraformOutcome(t, f, backend, deployment.ID, decision)
}

func checkLibraryTerraformContextRejected(
	t *testing.T, f *artifactE2E, backend *libraryTerraformState,
	deploymentID int64,
) {
	t.Helper()
	f.completion(t, deploymentID, events.DeploymentFailed)
	var gates []pages.ArtifactGateInfo
	decodeStepLogTest(
		t,
		f.api(t, "GET", gateAPIPath(deploymentID), nil, 200),
		&gates,
	)
	if len(gates) != 0 {
		t.Fatal("existing context reached artifact approval", gates)
	}
	backend.mutex.Lock()
	locks, writes := backend.locks, backend.writes
	backend.mutex.Unlock()
	if locks != 0 || writes != 0 {
		t.Fatal("existing context reached the Terraform backend")
	}
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs.txt", deploymentID),
		nil,
		200,
	)
	if !strings.Contains(string(logs), "Terraform context already exists") {
		t.Fatal("missing existing-context failure", string(logs))
	}
}

func checkLibraryTerraformOutcome(
	t *testing.T, f *artifactE2E, backend *libraryTerraformState,
	deploymentID int64, decision string,
) {
	t.Helper()
	backend.mutex.Lock()
	writes, locks := backend.writes, backend.locks
	state := string(backend.state)
	backend.mutex.Unlock()
	if decision == "approve" {
		if writes != 1 || locks < 2 ||
			!strings.Contains(state, "terraform_data") {
			t.Fatal(
				"approved plan did not use durable locked state",
				writes,
				locks,
			)
		}
	} else if writes != 0 {
		t.Fatal("rejected or stale plan wrote state")
	}
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs.txt", deploymentID),
			nil,
			200,
		),
	)
	if strings.Contains(logs, "fixture-backend-secret") {
		t.Fatal("Terraform logged a backend secret")
	}
	if decision == "approve" &&
		!strings.Contains(logs, "Approved Terraform plan applied") {
		t.Fatal("missing approved-plan marker")
	}
}

func libraryTerraformImage(
	t *testing.T,
	f *artifactE2E,
	backend string,
) string {
	t.Helper()
	context := t.TempDir()
	configuration := fmt.Sprintf(`terraform {
  backend "http" {
    address = %q
    lock_address = %q
    unlock_address = %q
  }
}
resource "terraform_data" "library" {
  input = "Saved plan approved through the template library"
}
`, backend+"/state", backend+"/lock", backend+"/lock")
	if err := os.WriteFile(filepath.Join(context, "main.tf"), []byte(configuration), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(context, ".terraform.lock.hcl"), []byte("# Built-in terraform_data needs no external provider.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	image := "localhost/durpdeploy-library-terraform:" + uuid.NewString()
	stagingRuntime(
		t,
		"build",
		"--tag",
		image,
		"--file",
		filepath.Join(
			"..",
			"..",
			"..",
			"scripts",
			"testdata",
			"template-library-terraform.Dockerfile",
		),
		context,
	)
	t.Cleanup(func() {
		f.h.runner.KillAll()
		ids := stagingRuntime(
			t,
			"ps",
			"--all",
			"--quiet",
			"--filter=ancestor="+image,
			"--filter=label=io.durpdeploy.namespace="+os.Getenv(
				"DURPDEPLOY_CONTAINER_RUNTIME",
			)+":"+os.Getenv(
				"DURPDEPLOY_CONTAINER_NAMESPACE",
			)+":gate-images",
		)
		if ids != "" {
			stagingRuntime(
				t,
				append(
					[]string{"rm", "--force", "--volumes"},
					strings.Fields(ids)...)...)
		}
		stagingRuntime(t, "image", "rm", image)
	})
	return image
}
