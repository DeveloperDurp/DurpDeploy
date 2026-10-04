//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestArtifactGateNonGatedPollingE2E(t *testing.T) {
	f := newVerificationE2E(t)
	deployment := verificationDeploy(t, f, verificationRelease(t, f, "plain"))
	panel := f.web(t, "GET", fmt.Sprintf(
		"/deployments/%d/artifact-gates", deployment.ID,
	), nil, 200)
	if strings.Contains(panel, "every 3s") {
		t.Fatal("a non-gated deployment polls for artifact gates")
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
}

func TestArtifactGateTerminalVerificationAndRerunE2E(t *testing.T) {
	for _, terminal := range []string{"rejected", "expired", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			// Given: verification was snapshotted before generation paused.
			f := newArtifactE2E(t)
			configureVerification(t, f, "bash", "true", 30)
			_, deployment, gate := createGateDeployment(t, f)
			path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
			switch terminal {
			case "rejected":
				f.api(t, "POST", path+"/artifact-gates/0/reject",
					map[string]any{"sha256": gate.SHA256, "revision": 1}, 200)
			case "cancelled":
				f.api(t, "POST", path+"/cancel", nil, 200)
			case "expired":
				if _, err := f.h.repo.DB.Exec(
					"UPDATE artifact_gates SET expires_at=0 WHERE deployment_id=?",
					deployment.ID,
				); err != nil {
					t.Fatal(err)
				}
				f.api(t, "GET", path+"/artifact-gates", nil, 200)
			}
			// Then: the API reports completed verification immediately.
			var verification db.DeploymentVerification
			if err := json.Unmarshal(f.api(t, "GET", path+"/verification",
				nil, 200), &verification); err != nil {
				t.Fatal(err)
			}
			want := "failed"
			if terminal == "cancelled" {
				want = "cancelled"
			}
			if verification.Status != want || !verification.FinishedAt.Valid {
				t.Fatalf("verification=%+v", verification)
			}
			webPath := fmt.Sprintf("/deployments/%d", deployment.ID)
			panel := f.web(t, "GET", webPath+"/artifact-gates", nil, 200)
			if strings.Contains(panel, "every 3s") {
				t.Fatal("terminal review still polls")
			}
			// The visible web rerun starts a fresh generation, as does the API.
			f.web(t, "POST", webPath+"/redeploy", url.Values{}, 303)
			var nextID int64
			if err := f.h.repo.DB.QueryRow("SELECT MAX(id) FROM deployments").
				Scan(&nextID); err != nil || nextID == deployment.ID {
				t.Fatalf("rerun=%d err=%v", nextID, err)
			}
			f.completion(t, nextID, events.ArtifactAwaitingApproval)
			f.api(t, "POST", fmt.Sprintf("/api/v1/deployments/%d/cancel",
				nextID), nil, 200)
		})
	}
}
