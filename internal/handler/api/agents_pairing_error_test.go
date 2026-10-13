package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/handler/api"
)

type failedAgentPairer struct{ err error }

func (p failedAgentPairer) Pair(
	context.Context,
	agentserver.PairingInput,
) (agentserver.PairingResult, error) {
	return agentserver.PairingResult{}, p.err
}

func TestAgentPairingFailureKeepsProtectedValuesPrivate(t *testing.T) {
	// Given: transport errors that contain protected pairing values.
	code := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	address := "https://private-agent.test:10943"
	fingerprint := strings.Repeat("a", 64)
	_, malformed := http.ReadResponse(bufio.NewReader(strings.NewReader(
		"HTTP/1.1 "+code+"\r\n",
	)), nil)
	if malformed == nil || !strings.Contains(malformed.Error(), code) {
		t.Fatal("malformed response did not contain the pairing code")
	}
	for _, test := range []struct {
		name string
		err  error
	}{
		{"malformed response", fmt.Errorf("pairing response: %w", malformed)},
		{"connection failure", &net.OpError{
			Op: "dial", Net: "tcp", Err: errors.New(address),
		}},
		{"fingerprint failure", errors.New(fingerprint)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			body, err := json.Marshal(map[string]string{
				"address": address, "code": code, "fingerprint": fingerprint,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost,
				"/api/v1/admin/agents/pair", bytes.NewReader(body))
			recorder := httptest.NewRecorder()

			// When: the API handles a failed pairing transport.
			api.NewAgentHandler(nil, failedAgentPairer{test.err}).PairAgent(
				recorder, request,
			)

			// Then: it returns 503 without exposing protected values.
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d", recorder.Code)
			}
			var response map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response["error"] == "" {
				t.Fatal("missing API error")
			}
			for _, protected := range []string{code, address, fingerprint} {
				if strings.Contains(logs.String(), protected) ||
					strings.Contains(recorder.Body.String(), protected) {
					t.Fatal("pairing failure exposed a protected value")
				}
			}
		})
	}
}
