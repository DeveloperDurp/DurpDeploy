package pages

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestAgentPairingConfirmationPageHidesWriteFormsFromViewer(
	t *testing.T,
) {
	// Given
	request := httptest.NewRequest(http.MethodGet, "/admin/agents/pair/id", nil)
	request = auth.SetUser(request, &db.User{Role: "viewer"})
	challenge := agentserver.PairingChallenge{
		ID: "challenge", Fingerprint: strings.Repeat("a", 64),
	}
	var rendered bytes.Buffer

	// When
	err := AgentPairingConfirmationPage(
		challenge,
		request.URL.Path,
	).Render(request.Context(), &rendered)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	if strings.Contains(body, "Approve agent") ||
		strings.Contains(body, ">Deny<") {
		t.Fatalf("viewer received pairing write controls: %s", body)
	}
	if !strings.Contains(body, "Viewers cannot confirm agent pairing") {
		t.Fatalf("viewer block message missing: %s", body)
	}
}
