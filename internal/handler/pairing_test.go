package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/migrate"
)

func TestPairingDisabledListenerReturns503NoState(t *testing.T) {
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	nextCalled := false
	handler := InternalErrorMiddleware(PairingPOSTHandler(
		nil,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			nextCalled = true
		}),
	))

	for range 2 {
		request := httptest.NewRequest(
			http.MethodPost,
			"/admin/agents/pair",
			strings.NewReader("pairing_code=secret&fingerprint=pin"),
		)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		if response.Code != http.StatusServiceUnavailable ||
			!strings.Contains(response.Body.String(), "listener is disabled") ||
			strings.Contains(response.Body.String(), "secret") {
			t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
		}
	}
	if nextCalled {
		t.Fatal("disabled pairing request reached endpoint")
	}
	var agents, pairings int
	if err := conn.QueryRowContext(
		context.Background(),
		"SELECT count(*) FROM agents",
	).Scan(&agents); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(
		context.Background(),
		"SELECT count(*) FROM agent_pairings",
	).Scan(&pairings); err != nil {
		t.Fatal(err)
	}
	if agents != 0 || pairings != 0 {
		t.Fatalf("agents=%d pairings=%d", agents, pairings)
	}
}

func TestPairingPOSTHandlerDelegatesWhenEnabled(t *testing.T) {
	nextCalled := false
	handler := PairingPOSTHandler(
		new(agentserver.PairingService),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/pair", nil),
	)

	if !nextCalled || response.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d", nextCalled, response.Code)
	}
}
