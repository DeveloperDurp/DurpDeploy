package dispatch

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/httpstream"
	"durpdeploy/internal/runner"
)

func TestStepLogCursorFollowsCommitOrder(t *testing.T) {
	engine := provisionLifecycleEngine(t, "PostgreSQL")
	first, second := openLifecycleEngine(t, engine)
	identity := seedRemoteLifecycle(t, first, 201)
	ctx := t.Context()
	tx, err := first.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	q := first.Queries.WithTx(tx)
	if _, err := q.LockDeploymentLogStream(ctx, identity.DeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateStepDeploymentLog(ctx, db.CreateStepDeploymentLogParams{DeploymentID: identity.DeploymentID, Line: "first-delayed"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := second.CreateStepDeploymentLog(
			ctx,
			db.CreateStepDeploymentLogParams{
				DeploymentID: identity.DeploymentID,
				Line:         "second-visible",
			},
		)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("second writer bypassed uncommitted first writer: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	broker := runner.NewLogBroker()
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpstream.StreamStepLogs(
				w,
				r,
				first,
				broker,
				identity.DeploymentID,
			)
		}),
	)
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second writer did not finish")
	}
	if _, err := first.DB.ExecContext(ctx, "UPDATE deployments SET status='succeeded' WHERE id=?", identity.DeploymentID); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	firstIndex, secondIndex, completeIndex := strings.Index(
		text,
		"first-delayed",
	), strings.Index(
		text,
		"second-visible",
	), strings.Index(
		text,
		"event: complete",
	)
	if firstIndex < 0 || secondIndex <= firstIndex ||
		completeIndex <= secondIndex {
		t.Fatalf("stream lost or reordered committed logs: %s", text)
	}
}
