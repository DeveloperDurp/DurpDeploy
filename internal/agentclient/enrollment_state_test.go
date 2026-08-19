package agentclient

import (
	"context"
	"net/http"
	"os"
	"testing"

	"durpdeploy/internal/agentproto"
)

func TestClient_skipsEnrollmentAfterSuccessfulEnrollment(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	enrollments := 0
	server := newTLSServer(
		t,
		serverIdentity,
		func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == agentproto.EnrollmentPath {
				enrollments++
			}
			writer.WriteHeader(http.StatusNoContent)
		},
	)
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("secure state directory: %v", err)
	}
	config := Config{
		ServerURL:         server.URL,
		ServerFingerprint: serverIdentity.Fingerprint.String(),
		StateDir:          stateDir,
		EnrollmentToken:   "enrollment-secret",
		AgentID:           "agent-a",
		Name:              "Agent A",
		AgentVersion:      "v1",
		Protocol:          string(agentproto.AgentV1),
	}
	first, err := New(config)
	if err != nil {
		t.Fatalf("new first client: %v", err)
	}
	defer first.Close()
	if err := first.Enroll(context.Background()); err != nil {
		t.Fatalf("first enrollment: %v", err)
	}
	config.EnrollmentToken = ""
	second, err := New(config)
	if err != nil {
		t.Fatalf("new restarted client: %v", err)
	}
	defer second.Close()

	// When
	err = second.Enroll(context.Background())

	// Then
	if err != nil || enrollments != 1 {
		t.Fatalf("restart enrollment error=%v requests=%d", err, enrollments)
	}
}
