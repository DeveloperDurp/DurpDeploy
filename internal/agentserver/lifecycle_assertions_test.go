package agentserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"durpdeploy/internal/db"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func (fixture *pollFixture) lifecycle(
	t *testing.T,
	identity agenttls.Identity,
	deploymentID int64,
	action, body string,
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
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+"/agent/v1/deployments/"+strconv.FormatInt(
			deploymentID,
			10,
		)+"/"+action,
		bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatalf("new lifecycle request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}).Do(
		request,
	)
	if err != nil {
		t.Fatalf("lifecycle request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func claimBody(
	token string,
) string {
	return `{"protocol":"agent/1","claim_token":"` + token + `"}`
}

func claimDispatch(
	t *testing.T,
	fixture *pollFixture,
	deploymentID int64,
	agentID, token string,
) {
	t.Helper()
	hash := sha256.Sum256([]byte(token))
	_, err := fixture.repo.Queries.ClaimDeploymentDispatch(
		context.Background(),
		db.ClaimDeploymentDispatchParams{
			AgentID:        sql.NullString{String: agentID, Valid: true},
			ClaimTokenHash: hash[:],
			ClaimExpiresAt: sql.NullInt64{
				Int64: fixture.now.AddDate(0, 0, 1).Unix(),
				Valid: true,
			},
			LastHeartbeatAt: sql.NullInt64{
				Int64: fixture.now.Unix(),
				Valid: true,
			},
			DeploymentID: deploymentID,
		},
	)
	if err != nil {
		t.Fatalf("claim dispatch: %v", err)
	}
}

func assertDispatchState(
	t *testing.T,
	fixture *pollFixture,
	deploymentID int64,
	want string,
) {
	t.Helper()
	dispatch, err := fixture.repo.Queries.GetDeploymentDispatch(
		context.Background(),
		deploymentID,
	)
	if err != nil || dispatch.State != want {
		t.Fatalf("dispatch state = %q, %v; want %q", dispatch.State, err, want)
	}
}

func assertDeploymentStatus(
	t *testing.T,
	fixture *pollFixture,
	deploymentID int64,
	want string,
) {
	t.Helper()
	deployment, err := fixture.repo.Queries.GetDeployment(
		context.Background(),
		deploymentID,
	)
	if err != nil || deployment.Status != want {
		t.Fatalf(
			"deployment status = %q, %v; want %q",
			deployment.Status,
			err,
			want,
		)
	}
}

func assertLogSequences(
	t *testing.T,
	fixture *pollFixture,
	deploymentID int64,
	want []int64,
) {
	t.Helper()
	rows, err := fixture.repo.DB.QueryContext(
		context.Background(),
		"SELECT sequence FROM deployment_logs WHERE deployment_id = ? ORDER BY sequence",
		deploymentID,
	)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var sequence int64
		if err := rows.Scan(&sequence); err != nil {
			t.Fatalf("scan sequence: %v", err)
		}
		got = append(got, sequence)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate logs: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("sequences = %v, want %v", got, want)
	}
}

func assertAgentEventCount(
	t *testing.T,
	fixture *pollFixture,
	deploymentID int64,
	eventType string,
	want int,
) {
	assertCount(t, fixture, "agent_events", deploymentID, eventType, want)
}

func assertNotificationCount(
	t *testing.T,
	fixture *pollFixture,
	deploymentID int64,
	eventType string,
	want int,
) {
	assertCount(
		t,
		fixture,
		"notification_events",
		deploymentID,
		eventType,
		want,
	)
}

func assertCount(
	t *testing.T,
	fixture *pollFixture,
	table string,
	deploymentID int64,
	eventType string,
	want int,
) {
	t.Helper()
	var got int
	if err := fixture.repo.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table+" WHERE deployment_id = ? AND event_type = ?", deploymentID, eventType).
		Scan(&got); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if got != want {
		t.Fatalf("events = %d, want %d", got, want)
	}
}
