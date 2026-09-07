package pages_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/deploymentstate"
	"durpdeploy/internal/events"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/notify"
	"durpdeploy/internal/repository"
)

func TestRootNotificationDeduplication(t *testing.T) {
	// Given
	ctx := context.Background()
	connection, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	repo := repository.New(connection)

	var receipts []string
	var receiptsMu sync.Mutex
	receiver := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		receiptsMu.Lock()
		receipts = append(receipts, payload.Text)
		receiptsMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)

	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "notification-project"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := repo.Queries.UpdateProjectNotifications(
		ctx,
		db.UpdateProjectNotificationsParams{
			SlackWebhookUrl: sql.NullString{String: receiver.URL, Valid: true},
			ID:              project.ID,
		},
	); err != nil {
		t.Fatalf("configure webhook: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "notification-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "notification-release",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	root, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create root deployment: %v", err)
	}
	children := make([]db.Deployment, 2)
	for index := range children {
		agentID := "agent-" + string(rune('a'+index))
		if _, err := repo.Queries.CreatePendingAgent(
			ctx,
			db.CreatePendingAgentParams{ID: agentID, Name: agentID},
		); err != nil {
			t.Fatalf("create agent %d: %v", index, err)
		}
		child, err := repo.Queries.CreateRoutingChildDeployment(
			ctx,
			db.CreateRoutingChildDeploymentParams{
				ReleaseID:     release.ID,
				EnvironmentID: environment.ID,
				Status:        "pending",
				ParentDeploymentID: sql.NullInt64{
					Int64: root.ID,
					Valid: true,
				},
				TargetAgentID: sql.NullString{
					String: agentID,
					Valid:  true,
				},
				TargetAgentName: sql.NullString{
					String: agentID,
					Valid:  true,
				},
			},
		)
		if err != nil {
			t.Fatalf("create child %d: %v", index, err)
		}
		children[index] = child
	}

	bus := events.NewBus(repo)
	bus.Register(notify.NewSlackNotifierWithClient(receiver.Client()))
	childEvent := func(typ events.Type, child db.Deployment) {
		bus.Publish(ctx, events.Event{
			Type:          typ,
			DeploymentID:  child.ID,
			ProjectID:     project.ID,
			EnvironmentID: environment.ID,
			Message:       "child lifecycle event",
		})
	}

	// When
	for _, child := range children {
		if err := repo.Queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:        child.ID,
				Status:    "running",
				StartedAt: sql.NullInt64{Int64: 10, Valid: true},
			},
		); err != nil {
			t.Fatalf("start child %d: %v", child.ID, err)
		}
		if err := deploymentstate.RecomputeParent(ctx, repo.Queries, child.ID); err != nil {
			t.Fatalf("recompute root after start: %v", err)
		}
		childEvent(events.DeploymentStarted, child)
	}
	childEvent(events.DeploymentStarted, children[0])

	if err := repo.Queries.FinishDeployment(
		ctx,
		db.FinishDeploymentParams{
			ID:         children[0].ID,
			Status:     "succeeded",
			FinishedAt: sql.NullInt64{Int64: 20, Valid: true},
		},
	); err != nil {
		t.Fatalf("finish first child: %v", err)
	}
	if err := deploymentstate.RecomputeParent(ctx, repo.Queries, children[0].ID); err != nil {
		t.Fatalf("recompute root after first result: %v", err)
	}
	childEvent(events.DeploymentSucceeded, children[0])

	if err := repo.Queries.FinishDeployment(
		ctx,
		db.FinishDeploymentParams{
			ID:         children[1].ID,
			Status:     "failed",
			FinishedAt: sql.NullInt64{Int64: 30, Valid: true},
		},
	); err != nil {
		t.Fatalf("finish second child: %v", err)
	}
	if err := deploymentstate.RecomputeParent(ctx, repo.Queries, children[1].ID); err != nil {
		t.Fatalf("recompute root after terminal result: %v", err)
	}
	childEvent(events.DeploymentSucceeded, children[1])
	childEvent(events.DeploymentSucceeded, children[1])
	if _, err := repo.Queries.CreateAgentEvent(
		ctx,
		db.CreateAgentEventParams{
			AgentID:      sql.NullString{String: "agent-b", Valid: true},
			DeploymentID: sql.NullInt64{Int64: children[1].ID, Valid: true},
			EventType:    "late_result",
		},
	); err != nil {
		t.Fatalf("record child-scoped agent event: %v", err)
	}

	// Then
	var rootNotifications int
	if err := repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM notification_events WHERE deployment_id = ?",
		root.ID,
	).Scan(&rootNotifications); err != nil {
		t.Fatalf("count root notifications: %v", err)
	}
	if rootNotifications != 2 {
		t.Fatalf("root notifications = %d, want 2", rootNotifications)
	}
	var childNotifications int
	if err := repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM notification_events WHERE deployment_id IN (?, ?)",
		children[0].ID,
		children[1].ID,
	).Scan(&childNotifications); err != nil {
		t.Fatalf("count child notifications: %v", err)
	}
	if childNotifications != 0 {
		t.Fatalf("child notifications = %d, want 0", childNotifications)
	}
	var childAgentEvents int
	if err := repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM agent_events WHERE deployment_id = ?",
		children[1].ID,
	).Scan(&childAgentEvents); err != nil {
		t.Fatalf("count child agent events: %v", err)
	}
	if childAgentEvents != 1 {
		t.Fatalf("child agent events = %d, want 1", childAgentEvents)
	}
	receiptsMu.Lock()
	defer receiptsMu.Unlock()
	if len(receipts) != 2 {
		t.Fatalf("webhook receipts = %d, want 2: %v", len(receipts), receipts)
	}
	for _, receipt := range receipts {
		if receipt == "" {
			t.Fatal("webhook receipt has empty text")
		}
	}
}
