package agentserver_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"

	agentbootstrap "github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentDeleteRejoinE2E(t *testing.T) {
	// Given: real admin and pinned agent servers, plus the same identity keys.
	f := newAgentFixture(t)
	history := seedPollPayload(t, f, "pending", "test-agent")
	listener, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: f.identityDir, ListenAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Shutdown(t.Context()) })
	box, err := secret.NewBox(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	pullEndpoint, err := agentproto.ParsePullEndpoint(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := agentserver.NewPairingService(agentserver.PairingConfig{
		Repository:   f.repo,
		Identity:     f.serverIdentity,
		PullEndpoint: pullEndpoint,
		Secrets:      box,
		Now:          time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := fleetAdminServer(t, f, manager)
	oldCode := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	parsed, err := agentproto.ParsePairingCode(oldCode)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := parsed.Hash()
	if _, err := f.repo.DB.Exec(`UPDATE agent_pairings
		SET pairing_code_hash=? WHERE agent_id='test-agent'`, oldHash[:]); err != nil {
		t.Fatal(err)
	}
	fleetRequest(t, srv, "DELETE", "/api/v1/admin/agents/test-agent",
		"admin", "", http.StatusNoContent)
	fleetRequest(t, srv, "GET", "/api/v1/admin/agents/test-agent",
		"admin", "", http.StatusNotFound)
	response, err := f.client.Post(f.server.URL+agentproto.PollPath,
		"application/json", strings.NewReader(pollBody))
	if err == nil {
		response.Body.Close()
		if response.StatusCode < http.StatusBadRequest {
			t.Fatalf("deleted agent poll status=%d", response.StatusCode)
		}
	}
	request := map[string]any{
		"address": listener.Endpoint(), "code": oldCode,
		"fingerprint": f.identity.Fingerprint.String(),
	}
	fleetRequest(t, srv, "POST", "/api/v1/admin/agents/pair", "admin",
		agentDeletePairBody(t, request), http.StatusConflict)
	request["code"] = base64.RawURLEncoding.EncodeToString(
		bytes.Repeat([]byte{8}, 32),
	)
	fleetRequest(t, srv, "POST", "/api/v1/admin/agents/pair", "admin",
		agentDeletePairBody(t, request), http.StatusConflict)
	fleetRequest(t, srv, "GET", "/api/v1/admin/agents/test-agent",
		"admin", "", http.StatusNotFound)
	request["code"] = listener.Offer().Code

	// When: an administrator pairs the same fingerprint with its fresh code.
	result := fleetRequest(t, srv, "POST", "/api/v1/admin/agents/pair", "admin",
		agentDeletePairBody(t, request), http.StatusCreated)
	var pairing agentserver.PairingResult
	if err := json.Unmarshal(result, &pairing); err != nil {
		t.Fatal(err)
	}

	// Then: a new ID is visible, history is detached, and polling works again.
	if pairing.AgentID == "test-agent" || pairing.AgentID == "" ||
		pairing.State != "paired" {
		t.Fatalf("rejoined result=%+v", pairing)
	}
	fleetRequest(
		t,
		srv,
		"GET",
		"/api/v1/admin/agents/"+pairing.AgentID,
		"admin",
		"",
		http.StatusOK,
	)
	fleetRequest(t, srv, "GET", "/api/v1/admin/agents/test-agent", "admin",
		"", http.StatusNotFound)
	deployment, err := f.repo.Queries.GetDeployment(t.Context(), history)
	if err != nil || deployment.AssignedAgentID.Valid ||
		deployment.Status != "failed" {
		t.Fatalf("history=%+v error=%v", deployment, err)
	}
	historyBody := fleetRequest(t, srv, "GET",
		"/api/v1/deployments/"+strconv.FormatInt(history, 10),
		"admin", "", http.StatusOK)
	var historyResult db.Deployment
	if err := json.Unmarshal(historyBody, &historyResult); err != nil {
		t.Fatal(err)
	}
	if historyResult.ID != history || historyResult.AssignedAgentID.Valid {
		t.Fatalf("API history=%+v", historyResult)
	}
	next := seedPollPayload(t, f, "pending", pairing.AgentID)
	poll := decodePollResponse(
		t,
		postAgent(t, f, agentproto.PollPath, pollBody),
	)
	if int64(poll.DeploymentID) != next {
		t.Fatalf("rejoined poll=%+v expected deployment=%d", poll, next)
	}
}

func agentDeletePairBody(
	t *testing.T,
	request map[string]any,
) string {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
