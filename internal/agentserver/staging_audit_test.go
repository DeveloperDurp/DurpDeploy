package agentserver_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentStagingAuditRetainsAuthenticatedActorAfterPurge(t *testing.T) {
	// Given a started claim bound to the fixture's real mTLS identity.
	f := newAgentFixture(t)
	id, claim := claimedRemoteStep(t, f)
	seedStageInput(t, f, id)
	var identity struct {
		Token string `json:"claim_token"`
	}
	if err := json.Unmarshal([]byte(claim), &identity); err != nil {
		t.Fatal(err)
	}
	response := postAgent(t, f,
		fmt.Sprintf("/agent/v1/deployments/%d/start", id), claim)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.StatusCode)
	}
	request, err := http.NewRequestWithContext(t.Context(), "POST",
		fmt.Sprintf("%s/agent/v1/deployments/%d/staging/upload",
			f.server.URL, id),
		bytes.NewReader(stageArchive(t, map[string]string{"result": "ok"})))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-DurpDeploy-Claim", identity.Token)
	request.Header.Set("X-Agent-ID", "spoofed-agent")

	// When the agent publishes and terminal cleanup removes its snapshot.
	response, err = f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal(response.StatusCode)
	}
	if err := f.repo.WithDeploymentTx(t.Context(), id,
		func(ctx context.Context, q *db.Queries) error {
			return q.UpdateDeploymentStatus(ctx,
				db.UpdateDeploymentStatusParams{ID: id, Status: "succeeded"})
		}); err != nil {
		t.Fatal(err)
	}

	// Then the audit retains the authenticated actor and deployment ID.
	var details string
	var userID sql.NullInt64
	if err := f.repo.DB.QueryRow(`SELECT user_id, details FROM audit_log
WHERE action='publish_staging' AND entity_id=?`, id).
		Scan(&userID, &details); err != nil {
		t.Fatal(err)
	}
	var actor struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(details), &actor); err != nil {
		t.Fatal(err)
	}
	if userID.Valid || actor.AgentID != "test-agent" {
		t.Fatalf("user=%v agent=%q", userID, actor.AgentID)
	}
}
