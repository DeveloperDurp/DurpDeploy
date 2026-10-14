//go:build e2e && agentstaging

package agentserver_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func TestDeploymentStagingArtifactGateAPIE2E(t *testing.T) {
	s := newStagingE2E(t)
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			base, environment := s.packageProject(t, "gate-"+decision)
			steps := []map[string]any{
				{
					"name": "Prepare on agent", "execution_target": "agent",
					"agent_selectors": []string{"test-agent"},
					"script_body": `set -eu
printf before > "$DURPDEPLOY_STAGE_DIR/before"
echo mixed-gate-prepared-once`,
				},
				{
					"name": "Plan", "container_image": "docker.io/library/bash:5.2",
					"approval_artifact_path": "plan",
					"approval_review_path":   "review",
					"approval_review_format": "summary",
					"script_body": `set -eu
test "$(cat "$DURPDEPLOY_STAGE_DIR/before")" = before
printf approved-plan > "$DURPDEPLOY_STAGE_DIR/plan"
printf '{"create":1}' > "$DURPDEPLOY_STAGE_DIR/review"`,
				},
				{
					"name": "Apply", "container_image": "docker.io/library/bash:5.2",
					"script_body": `set -eu
test "$(cat "$DURPDEPLOY_APPROVED_DIR/plan")" = approved-plan
test "$(cat "$DURPDEPLOY_APPROVED_DIR/before")" = before
if printf changed > "$DURPDEPLOY_APPROVED_DIR/plan"; then exit 8; fi
printf applied > "$DURPDEPLOY_STAGE_DIR/result"`,
				},
				{
					"name":             "Deliver in agent container",
					"execution_target": "agent", "agent_execution_mode": "container",
					"container_image": "docker.io/library/bash:5.2",
					"agent_selectors": []string{"second-agent"},
					"script_body": `set -eu
test "$(cat "$DURPDEPLOY_STAGE_DIR/result")" = applied
printf delivered > "$DURPDEPLOY_STAGE_DIR/result"`,
				},
				{
					"name": "Verify on agent", "execution_target": "agent",
					"agent_selectors": []string{"test-agent"},
					"script_body": `set -eu
test "$(cat "$DURPDEPLOY_STAGE_DIR/result")" = delivered
echo mixed-gate-delivery-complete`,
				},
			}
			for _, step := range steps {
				s.api(t, "POST", base+"/steps", step, 201)
			}
			var release, deployment struct{ ID int64 }
			if err := json.Unmarshal(s.api(t, "POST", base+"/releases",
				map[string]string{"version": "1"}, 201), &release); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(s.api(t, "POST", base+"/deployments",
				map[string]int64{"release_id": release.ID,
					"environment_id": environment}, 201), &deployment); err != nil {
				t.Fatal(err)
			}
			s.wait(t, deployment.ID, "awaiting_artifact_approval")
			var laterRuns int
			if err := s.f.repo.DB.QueryRowContext(t.Context(),
				"SELECT COUNT(*) FROM remote_step_runs WHERE deployment_id=? AND step_index>1",
				deployment.ID).Scan(&laterRuns); err != nil || laterRuns != 0 {
				t.Fatalf(
					"agent delivery ran before approval: %d %v",
					laterRuns,
					err,
				)
			}
			for _, index := range []int64{0, 3, 4} {
				if _, err := s.f.repo.Queries.GetArtifactGateImage(t.Context(),
					db.GetArtifactGateImageParams{DeploymentID: deployment.ID,
						StepIndex: index}); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf(
						"agent step %d was pinned on the server: %v",
						index,
						err,
					)
				}
			}
			gatePath := fmt.Sprintf(
				"/deployments/%d/artifact-gates",
				deployment.ID,
			)
			var gates []pages.ArtifactGateInfo
			if err := json.Unmarshal(s.api(t, "GET", gatePath, nil, 200),
				&gates); err != nil || len(gates) != 1 || gates[0].StepIndex != 1 {
				t.Fatalf("artifact gates=%+v err=%v", gates, err)
			}
			s.api(t, "POST", gatePath+"/1/"+decision, map[string]any{
				"revision": gates[0].Revision, "sha256": gates[0].SHA256,
			}, 200)
			want := "succeeded"
			if decision == "reject" {
				want = "rejected"
			}
			s.wait(t, deployment.ID, want)
			logs := string(s.api(
				t,
				"GET",
				fmt.Sprintf(
					"/deployments/%d/logs.txt",
					deployment.ID,
				),
				nil,
				200,
			))
			if strings.Contains(logs, "mixed-gate-delivery-complete") !=
				(decision == "approve") {
				t.Fatalf("decision=%s delivery outcome=%s", decision, logs)
			}
			if strings.Count(logs, "mixed-gate-prepared-once") != 1 {
				t.Fatalf("pre-approval agent step was replayed: %s", logs)
			}
			s.assertRuntimeStagingPurged(t)
		})
	}
}
