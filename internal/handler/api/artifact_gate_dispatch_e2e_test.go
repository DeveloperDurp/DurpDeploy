//go:build e2e

package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/handler"
)

type disconnectAfterApprovalDB struct {
	*sql.DB
	request      context.Context
	cancel       context.CancelFunc
	deploymentID int64
	enabled      atomic.Bool
}

func (d *disconnectAfterApprovalDB) QueryRowContext(
	ctx context.Context, query string, args ...any,
) *sql.Row {
	if d.enabled.Load() && ctx == d.request {
		var status string
		if err := d.DB.QueryRow("SELECT status FROM deployments WHERE id=?",
			d.deploymentID).Scan(&status); err == nil && status == "pending" {
			// Inject a disconnected request at the first read after approval.
			d.cancel()
		}
	}
	return d.DB.QueryRowContext(ctx, query, args...)
}

type disconnectedGateResponse struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w disconnectedGateResponse) Write(data []byte) (int, error) {
	w.cancel()
	return w.ResponseRecorder.Write(data)
}

func TestArtifactGateDispatchSurvivesDisconnectE2E(t *testing.T) {
	f := newArtifactE2E(t)
	disconnect := &disconnectAfterApprovalDB{DB: f.h.repo.DB}
	f.h.repo.Queries = db.New(disconnect)
	_, deployment, gate := createGateDeployment(t, f)
	user := seedAPIUser(t, f.h.repo, "disconnect@gate.example", "admin")
	data, err := json.Marshal(map[string]any{
		"revision": gate.Revision, "sha256": gate.SHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, "POST",
		gateAPIPath(deployment.ID)+"/0/approve", bytes.NewReader(data))
	req = withAPIUser(req, user)
	req = withAPIURLParam(req, "id", fmt.Sprint(deployment.ID))
	req = withAPIURLParam(req, "stepIndex", "0")
	disconnect.request = req.Context()
	disconnect.cancel = cancel
	disconnect.deploymentID = deployment.ID
	disconnect.enabled.Store(true)
	response := httptest.NewRecorder()
	handler.NewArtifactGateHandler(f.h.repo, f.h.runner).Approve(
		disconnectedGateResponse{response, cancel}, req,
	)
	if ctx.Err() == nil || response.Code != 200 {
		t.Fatalf("disconnected approval response=%d", response.Code)
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
}
