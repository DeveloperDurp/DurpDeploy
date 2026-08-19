package agentserver

import (
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
)

func TestEnrollment_activatesPendingAgentOnceOverPinnedTLS(t *testing.T) {
	// Given
	fixture := newEnrollmentFixture(t)
	fixture.createPendingAgent(t, "agent-a")
	fixture.createToken(
		t,
		"agent-a",
		fixture.token,
		fixture.now.Add(time.Minute),
	)

	// When
	response := fixture.enroll(
		t,
		"agent-a",
		fixture.agentIdentity,
		fixture.token,
	)

	// Then
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf(
			"enroll status = %d, want %d",
			response.StatusCode,
			http.StatusNoContent,
		)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"Cache-Control = %q, want no-store",
			response.Header.Get("Cache-Control"),
		)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read enrollment response: %v", err)
	}
	if strings.Contains(string(body), fixture.token) ||
		strings.Contains(string(body), "PRIVATE KEY") {
		t.Fatal("enrollment response leaked credential material")
	}
	assertActiveAgent(
		t,
		fixture.repo,
		"agent-a",
		fixture.agentIdentity.Fingerprint.String(),
	)

	// When
	replay := fixture.enroll(
		t,
		"agent-a",
		fixture.agentIdentity,
		fixture.token,
	)

	// Then
	if replay.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"replay status = %d, want %d",
			replay.StatusCode,
			http.StatusUnauthorized,
		)
	}
}

func TestEnrollment_rejectsInvalidCredentialsAndCertificates(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(t *testing.T, fixture *enrollmentFixture)
		agentID  string
		identity agenttls.Identity
		token    string
		body     func(fixture *enrollmentFixture, agentID string) string
	}{
		{
			name: "expired token",
			prepare: func(t *testing.T, fixture *enrollmentFixture) {
				fixture.createPendingAgent(t, "agent-a")
				fixture.createToken(t, "agent-a", fixture.token, fixture.now)
			},
			agentID:  "agent-a",
			identity: agenttls.Identity{},
			token:    fixtureToken,
		},
		{
			name: "wrong agent",
			prepare: func(t *testing.T, fixture *enrollmentFixture) {
				fixture.createPendingAgent(t, "agent-a")
				fixture.createPendingAgent(t, "agent-b")
				fixture.createToken(
					t,
					"agent-a",
					fixture.token,
					fixture.now.Add(time.Minute),
				)
			},
			agentID: "agent-b",
			token:   fixtureToken,
		},
		{
			name: "malformed certificate",
			prepare: func(t *testing.T, fixture *enrollmentFixture) {
				fixture.createPendingAgent(t, "agent-a")
				fixture.createToken(
					t,
					"agent-a",
					fixture.token,
					fixture.now.Add(time.Minute),
				)
			},
			agentID: "agent-a",
			token:   fixtureToken,
			body: func(_ *enrollmentFixture, agentID string) string {
				return enrollmentJSON(agentID, "not-a-certificate")
			},
		},
		{
			name: "certificate ownership mismatch",
			prepare: func(t *testing.T, fixture *enrollmentFixture) {
				fixture.createPendingAgent(t, "agent-a")
				fixture.createToken(
					t,
					"agent-a",
					fixture.token,
					fixture.now.Add(time.Minute),
				)
			},
			agentID: "agent-a",
			token:   fixtureToken,
			body: func(fixture *enrollmentFixture, agentID string) string {
				return enrollmentJSON(
					agentID,
					certificatePEM(t, fixture.serverIdentity.Certificate),
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture := newEnrollmentFixture(t)
			test.prepare(t, fixture)
			identity := test.identity
			if len(identity.Certificate.Certificate) == 0 {
				identity = fixture.agentIdentity
			}
			body := enrollmentJSON(
				test.agentID,
				certificatePEM(t, identity.Certificate),
			)
			if test.body != nil {
				body = test.body(fixture, test.agentID)
			}

			// When
			response := fixture.request(t, identity, test.token, body)

			// Then
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf(
					"status = %d, want %d",
					response.StatusCode,
					http.StatusUnauthorized,
				)
			}
		})
	}
}

func TestEnrollment_rejectsDuplicateFingerprintAndOversizeBody(t *testing.T) {
	// Given
	fixture := newEnrollmentFixture(t)
	fixture.createPendingAgent(t, "agent-a")
	fixture.createPendingAgent(t, "agent-b")
	fixture.createToken(
		t,
		"agent-a",
		fixture.token,
		fixture.now.Add(time.Minute),
	)
	fixture.createToken(
		t,
		"agent-b",
		"second-token",
		fixture.now.Add(time.Minute),
	)
	if response := fixture.enroll(
		t,
		"agent-a",
		fixture.agentIdentity,
		fixture.token,
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf("first enrollment status = %d", response.StatusCode)
	}

	// When
	duplicate := fixture.enroll(
		t,
		"agent-b",
		fixture.agentIdentity,
		"second-token",
	)
	overseized := fixture.request(
		t,
		fixture.agentIdentity,
		"second-token",
		strings.Repeat("x", agentproto.MaxRequestBytes+1),
	)

	// Then
	if duplicate.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"duplicate status = %d, want %d",
			duplicate.StatusCode,
			http.StatusUnauthorized,
		)
	}
	if overseized.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"oversize status = %d, want %d",
			overseized.StatusCode,
			http.StatusRequestEntityTooLarge,
		)
	}
}

func TestEnrollment_rejectsUnpinnedServer(t *testing.T) {
	// Given
	fixture := newEnrollmentFixture(t)
	clientTLS, err := agenttls.NewClientConfig(
		fixture.server.URL,
		[]agenttls.Fingerprint{fixture.agentIdentity.Fingerprint},
	)
	if err != nil {
		t.Fatalf("new wrongly pinned client config: %v", err)
	}
	clientTLS.Certificates = []tls.Certificate{
		fixture.agentIdentity.Certificate,
	}
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+agentproto.EnrollmentPath,
		strings.NewReader(enrollmentJSON(
			"agent-a",
			certificatePEM(t, fixture.agentIdentity.Certificate),
		)),
	)
	if err != nil {
		t.Fatalf("new enrollment request: %v", err)
	}
	request.Header.Set("Authorization", "Enrollment "+fixture.token)

	// When
	_, err = (&http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
	}).Do(request)
	if err == nil {
		t.Fatal("request to unpinned server succeeded")
	}

	// Then
}

func TestEnrollment_allowsOneConcurrentSuccess(t *testing.T) {
	// Given
	fixture := newEnrollmentFixture(t)
	fixture.createPendingAgent(t, "agent-a")
	fixture.createToken(
		t,
		"agent-a",
		fixture.token,
		fixture.now.Add(time.Minute),
	)
	start := make(chan struct{})
	results := make(chan int, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			results <- fixture.enrollStatus()
		}()
	}

	// When
	close(start)
	waitGroup.Wait()
	close(results)

	// Then
	successes := 0
	for status := range results {
		if status == http.StatusNoContent {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent enrollments = %d, want 1", successes)
	}
}
