package agentserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/events"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/notify"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestLifecycle_FanoutCancellationNotifiesRootOnceWithoutChildNotices(
	t *testing.T,
) {
	connection, err := migrate.Run(
		"file:" + filepath.Join(t.TempDir(), "fanout.db") +
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	repo := repository.New(connection)
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(
		ctx, db.CreateProjectParams{Name: "fanout"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	var receipts int
	receiver := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		receipts++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
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
		ctx, db.CreateEnvironmentParams{Name: "production"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	parent, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	children := make([]db.Deployment, 3)
	tokens := make([]agentproto.ClaimToken, 3)
	for index := range children {
		agentID := "agent-" + string(rune('a'+index))
		if _, err := repo.Queries.CreatePendingAgent(
			ctx, db.CreatePendingAgentParams{ID: agentID, Name: agentID},
		); err != nil {
			t.Fatalf("create agent: %v", err)
		}
		child, err := repo.Queries.CreateRoutingChildDeployment(
			ctx,
			db.CreateRoutingChildDeploymentParams{
				ReleaseID: release.ID, EnvironmentID: environment.ID,
				Status: "pending",
				ParentDeploymentID: sql.NullInt64{
					Int64: parent.ID, Valid: true,
				},
				TargetAgentID: sql.NullString{String: agentID, Valid: true},
				TargetAgentName: sql.NullString{
					String: agentID, Valid: true,
				},
			},
		)
		if err != nil {
			t.Fatalf("create child: %v", err)
		}
		children[index] = child
		if _, err := repo.Queries.CreateDirectDeploymentDispatch(
			ctx,
			db.CreateDirectDeploymentDispatchParams{
				DeploymentID:    child.ID,
				AssignedAgentID: sql.NullString{String: agentID, Valid: true},
			},
		); err != nil {
			t.Fatalf("create dispatch: %v", err)
		}
		token := agentproto.ClaimToken("token-" + agentID)
		tokens[index] = token
		hash := sha256.Sum256([]byte(token))
		if _, err := repo.Queries.ClaimDeploymentDispatch(
			ctx,
			db.ClaimDeploymentDispatchParams{
				DeploymentID:    child.ID,
				AgentID:         sql.NullString{String: agentID, Valid: true},
				ClaimTokenHash:  hash[:],
				ClaimExpiresAt:  sql.NullInt64{Int64: 100, Valid: true},
				LastHeartbeatAt: sql.NullInt64{Int64: 1, Valid: true},
			},
		); err != nil {
			t.Fatalf("claim child: %v", err)
		}
	}

	now := time.Unix(10, 0)
	bus := events.NewBus(repo)
	bus.Register(notify.NewSlackNotifierWithClient(receiver.Client()))
	server := &Server{
		repository: repo,
		events:     bus,
		now:        func() time.Time { return now },
	}
	for index, child := range children {
		agentID := "agent-" + string(rune('a'+index))
		if err := server.startDeployment(
			ctx, child.ID, agentID, tokens[index],
		); err != nil {
			t.Fatalf("start child: %v", err)
		}
		if _, err := server.writeLogs(
			ctx, child.ID, agentID,
			agentproto.LogBatchRequest{
				ClaimToken: tokens[index],
				Events:     []agentproto.LogEvent{{Sequence: 1, Line: agentID}},
			},
		); err != nil {
			t.Fatalf("write child log: %v", err)
		}
	}
	if err := server.startDeployment(
		ctx, children[0].ID, "agent-b", tokens[0],
	); !errors.Is(err, errLifecycleConflict) {
		t.Fatalf("wrong-agent start error = %v", err)
	}

	state, err := dispatch.NewCancellationService(repo, nil).Cancel(
		ctx, parent.ID,
	)
	if err != nil || state != "running" {
		t.Fatalf("request parent cancellation = %q, %v", state, err)
	}
	for index, child := range children {
		now = time.Unix(int64(20+index*10), 0)
		agentID := "agent-" + string(rune('a'+index))
		if err := server.cancelDeployment(
			ctx, child.ID, agentID, tokens[index],
		); err != nil {
			t.Fatalf("cancel child: %v", err)
		}
	}
	got, err := repo.Queries.GetDeployment(ctx, parent.ID)
	if err != nil || got.Status != "cancelled" || got.StartedAt.Int64 != 10 ||
		got.FinishedAt.Int64 != 40 {
		t.Fatalf("parent = %#v, %v; want cancelled [10,40]", got, err)
	}
	if err := server.cancelDeployment(
		ctx, children[0].ID, "agent-a", tokens[0],
	); err != nil {
		t.Fatalf("duplicate cancellation error = %v", err)
	}
	var rootNotifications int
	if err := repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM notification_events WHERE deployment_id = ?",
		parent.ID,
	).Scan(&rootNotifications); err != nil {
		t.Fatalf("count root notifications: %v", err)
	}
	if rootNotifications != 2 {
		t.Fatalf("root notifications = %d, want 2", rootNotifications)
	}
	var childNotifications int
	if err := repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM notification_events WHERE deployment_id IN (?, ?, ?)",
		children[0].ID,
		children[1].ID,
		children[2].ID,
	).Scan(&childNotifications); err != nil {
		t.Fatalf("count child notifications: %v", err)
	}
	if childNotifications != 0 {
		t.Fatalf("child notifications = %d, want 0", childNotifications)
	}
	if receipts != 2 {
		t.Fatalf("webhook receipts = %d, want 2", receipts)
	}
	t.Logf(
		"webhook receipts: root_id=%d types=deployment_started,deployment_cancelled count=%d child_ids=%d,%d,%d child_notifications=%d",
		parent.ID,
		receipts,
		children[0].ID,
		children[1].ID,
		children[2].ID,
		childNotifications,
	)
}
