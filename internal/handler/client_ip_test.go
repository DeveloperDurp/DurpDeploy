package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/requestmeta"
)

func TestLoginMetadataUsesTrustedForwardedClientIP(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	var sessionIP string
	var detailsJSON string
	handler := requestmeta.Middleware("192.0.2.0/24")(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			issue := (&AuthHandler{}).finalBrowserSessionIssue(
				r,
				finalSessionIssue{UserID: 1, Factor: finalLoginPassword},
			)
			sessionIP = issue.IPAddress
			detailsJSON = loginDetails(r, finalLoginPassword)
		}),
	)

	// When
	handler.ServeHTTP(httptest.NewRecorder(), request)

	// Then
	if sessionIP != "203.0.113.9" {
		t.Fatalf("session IP = %q, want forwarded client", sessionIP)
	}
	var details struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
		t.Fatalf("unmarshal login details: %v", err)
	}
	if details.IP != "203.0.113.9" {
		t.Fatalf("login audit IP = %q, want forwarded client", details.IP)
	}
}
