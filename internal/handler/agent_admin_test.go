package handler_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestViewerAgentControlsHidden(t *testing.T) {
	h := newProjectHarness(t)
	h.makeEnv("viewer environment")
	h.setRole("viewer")

	response, err := h.authedClient().Get(h.server.URL + "/environments")
	if err != nil {
		t.Fatalf("GET /environments: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK ||
		strings.Contains(string(body), "name=\"agent_id\"") ||
		strings.Contains(string(body), "/admin/environments/") {
		t.Fatalf("status=%d viewer controls rendered", response.StatusCode)
	}
}

func TestViewerAgentWriteReturnsToast(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("viewer")
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		h.server.URL+"/admin/environments/1/agent",
		strings.NewReader("agent_id=agent-a"),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("PUT environment agent: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(response.Header.Get("HX-Trigger"), "makeToast") {
		t.Fatalf(
			"status=%d trigger=%q",
			response.StatusCode,
			response.Header.Get("HX-Trigger"),
		)
	}
}
