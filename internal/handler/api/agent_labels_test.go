package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
)

func TestAgentLabelAPI_SerializesOnlySafeMemberFields(t *testing.T) {
	// Given
	h := newAPIHarness(t)
	label, err := h.repo.Queries.CreateAgentLabel(context.Background(),
		db.CreateAgentLabelParams{Name: "Cat Fact", NormalizedName: "cat fact"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	if _, err := h.repo.DB.ExecContext(context.Background(), `
INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint)
VALUES ('safe-agent', 'Safe Agent', 'active', 'private-certificate',
 'ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff');
INSERT INTO agent_pairings (
 agent_id, pairing_code_hash, agent_public_identity, agent_pin,
 server_public_identity, server_pin, state, expires_at, paired_at
) VALUES ('safe-agent',
 X'0000000000000000000000000000000000000000000000000000000000000000',
 'agent-public', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
 'server-public', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
 'paired', 2000000000, 1000000000);
INSERT INTO agent_label_memberships (agent_label_id, agent_id) VALUES (?, 'safe-agent')`,
		label.ID); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	request := withAPIURLParam(httptest.NewRequest(
		http.MethodGet, "/api/v1/admin/agent-labels/1", nil,
	), "labelID", "1")
	recorder := httptest.NewRecorder()

	// When
	handler.NewAgentAdminHandler(h.repo).GetAgentLabel(recorder, request)

	// Then
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{
		"private-certificate", "fingerprint", "agent-public", "agent-pin",
		"server-public", "server-pin",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Errorf(
				"response leaked %q: %s",
				forbidden,
				recorder.Body.String(),
			)
		}
	}
}
