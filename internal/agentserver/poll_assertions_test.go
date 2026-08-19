package agentserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"testing"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
)

func (fixture *pollFixture) poll(
	t *testing.T,
	identity agenttls.Identity,
	body string,
) *http.Response {
	t.Helper()
	clientTLS, err := agenttls.NewClientConfig(
		fixture.server.URL,
		[]agenttls.Fingerprint{fixture.serverIdentity.Fingerprint},
	)
	if err != nil {
		t.Fatalf("new pinned client config: %v", err)
	}
	clientTLS.Certificates = []tls.Certificate{identity.Certificate}
	request, err := http.NewRequest(http.MethodPost,
		fixture.server.URL+agentproto.PollPath, bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatalf("new poll request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}).Do(
		request,
	)
	if err != nil {
		t.Fatalf("poll request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func (fixture *pollFixture) pollStatus(identity agenttls.Identity) int {
	clientTLS, err := agenttls.NewClientConfig(
		fixture.server.URL,
		[]agenttls.Fingerprint{fixture.serverIdentity.Fingerprint},
	)
	if err != nil {
		return 0
	}
	clientTLS.Certificates = []tls.Certificate{identity.Certificate}
	request, err := http.NewRequest(http.MethodPost,
		fixture.server.URL+agentproto.PollPath,
		bytes.NewBufferString(`{"protocol":"agent/1","agent_version":"v1"}`),
	)
	if err != nil {
		return 0
	}
	response, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}).Do(
		request,
	)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	return response.StatusCode
}

func (fixture *pollFixture) assertClaim(
	t *testing.T,
	deploymentID int64,
	agentID string,
	hash []byte,
) {
	t.Helper()
	dispatch, err := fixture.repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil {
		t.Fatalf("get claimed dispatch: %v", err)
	}
	if dispatch.AgentID.String != agentID ||
		!bytes.Equal(dispatch.ClaimTokenHash, hash) {
		t.Fatalf(
			"dispatch owner/token hash = %q/%x",
			dispatch.AgentID.String,
			dispatch.ClaimTokenHash,
		)
	}
}

func (fixture *pollFixture) assertAgentVersion(
	t *testing.T,
	agentID, version string,
) {
	t.Helper()
	var stored string
	if err := fixture.repo.DB.QueryRowContext(context.Background(),
		"SELECT agent_version FROM agents WHERE id = ?", agentID,
	).Scan(&stored); err != nil {
		t.Fatalf("read agent version: %v", err)
	}
	if stored != version {
		t.Fatalf("agent version = %q, want %q", stored, version)
	}
}
