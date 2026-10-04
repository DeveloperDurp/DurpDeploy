//go:build e2e

package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"durpdeploy/internal/events"
	"durpdeploy/views/pages"
	"github.com/google/uuid"
)

func TestArtifactGateTerraformNetworkE2E(t *testing.T) {
	t.Run("apply", func(t *testing.T) { terraformGateNetwork(t, false) })
	t.Run("stale state", func(t *testing.T) { terraformGateNetwork(t, true) })
}

func terraformGateNetwork(t *testing.T, stale bool) {
	t.Helper()
	// Given: real Terraform, a networked HTTP state backend with locking,
	// and two API-created immutable plan/apply steps.
	f := newArtifactE2E(t)
	image := "localhost/durpdeploy-gate-terraform:" + uuid.NewString()
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
			"artifact-gate-terraform.Dockerfile",
		),
		filepath.Join("..", "..", ".."),
	)
	t.Cleanup(func() { stagingRuntime(t, "image", "rm", image) })
	var mutex sync.Mutex
	var state []byte
	var writes, locks int
	backend := verificationUpstream(
		t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mutex.Lock()
			defer mutex.Unlock()
			switch r.Method {
			case "GET":
				if state == nil {
					w.WriteHeader(404)
				} else {
					if _, err := w.Write(state); err != nil {
						return
					}
				}
			case "POST":
				var err error
				state, err = io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(500)
					return
				}
				writes++
			case "LOCK":
				locks++
			case "UNLOCK":
			default:
				w.WriteHeader(405)
			}
		}),
	)
	if os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") == "podman" {
		address, err := url.Parse(backend)
		if err != nil {
			t.Fatal(err)
		}
		backend = "http://host.containers.internal:" + address.Port()
	}
	for name, value := range map[string]string{"TF_HTTP_ADDRESS": backend + "/state", "TF_HTTP_LOCK_ADDRESS": backend + "/lock", "TF_HTTP_UNLOCK_ADDRESS": backend + "/lock"} {
		f.api(
			t,
			"POST",
			f.base()+"/variables",
			map[string]string{"name": name, "value": value},
			201,
		)
	}
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name":                   "Terraform plan",
		"container_image":        image,
		"network_mode":           "bridge",
		"approval_artifact_path": "context/tfplan",
		"approval_review_path":   "context/review.json",
		"approval_review_format": "terraform",
		"script_body": `set -eu
mkdir /tmp/context
cd /tmp/context
cat > /tmp/terraform.rc <<'RC'
provider_installation {
  filesystem_mirror { path = "/opt/providers" }
}
RC
export TF_CLI_CONFIG_FILE=/tmp/terraform.rc
cat > main.tf <<'TF'
terraform {
  backend "http" {}
  required_providers {
    random = {
      source = "hashicorp/random"
      version = "3.7.2"
    }
  }
}
resource "random_password" "example" { length = 16 }
output "password" {
  value = random_password.example.result
  sensitive = true
}
TF
terraform init -input=false
terraform plan -input=false -out=tfplan
terraform show -json tfplan > review.json
cp -a /tmp/context "$DURPDEPLOY_STAGE_DIR/context"`,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name":            "Terraform apply",
		"container_image": image,
		"network_mode":    "bridge",
		"script_body": `set -eu
cp -a "$DURPDEPLOY_APPROVED_DIR/context" /tmp/context
cd /tmp/context
test -f .terraform.lock.hcl
# A real provider and context larger than the ordinary 64 MiB scratch limit.
dd if=/dev/zero of=/tmp/scratch bs=1M count=70
terraform apply -input=false "$DURPDEPLOY_APPROVED_DIR/context/tfplan"`,
	}, 201)
	deployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "terraform-network"),
	)
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	var gates []pages.ArtifactGateInfo
	if err := json.Unmarshal(
		f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
		&gates,
	); err != nil ||
		len(gates) != 1 {
		t.Fatalf("gates=%v err=%v", gates, err)
	}
	gate := gates[0]
	data := f.api(t, "GET", gateAPIPath(deployment.ID)+"/0/artifact", nil, 200)
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != gate.SHA256 || gate.Review.Create != 1 {
		t.Fatal("plan identity/review mismatch")
	}
	mutex.Lock()
	before := writes
	mutex.Unlock()
	if before != 0 {
		t.Fatal("Terraform applied before approval")
	}
	if stale {
		mutex.Lock()
		state = []byte(
			`{"version":4,"terraform_version":"1.13.5","serial":1,"lineage":"ad93c268-710a-49a0-841b-02ce7a28e124","outputs":{},"resources":[]}`,
		)
		mutex.Unlock()
	}
	// When: this saved plan is approved through the public API.
	f.api(
		t,
		"POST",
		gateAPIPath(deployment.ID)+"/0/approve",
		map[string]any{"sha256": gate.SHA256, "revision": 1},
		200,
	)
	// Then: Terraform applies it over the network, with backend locking.
	if stale {
		f.completion(t, deployment.ID, events.DeploymentFailed)
		mutex.Lock()
		defer mutex.Unlock()
		if writes != 0 {
			t.Fatal("a stale saved plan changed the backend")
		}
		return
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	mutex.Lock()
	defer mutex.Unlock()
	if writes != 1 || locks < 2 || len(state) == 0 {
		t.Fatalf("backend writes=%d locks=%d", writes, locks)
	}
	var saved struct {
		Outputs map[string]struct {
			Value string `json:"value"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(state, &saved); err != nil {
		t.Fatal("invalid saved state")
	}
	password := saved.Outputs["password"].Value
	if password == "" {
		t.Fatal("external provider did not generate a result")
	}
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs.txt", deployment.ID),
		nil,
		200,
	)
	if len(logs) == 0 ||
		strings.Contains(string(logs), password) {
		t.Fatal("unsafe or missing progress logs")
	}
	if strings.Contains(
		string(f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200)),
		password,
	) {
		t.Fatal("generated sensitive value leaked into review")
	}
}
