package agentserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestLifecycle_FanoutAggregatesIndependentChildrenWithoutSockets(
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
	server := &Server{repository: repo, now: func() time.Time { return now }}
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

	states := []agentproto.ResultState{
		agentproto.ResultSucceeded,
		agentproto.ResultFailed,
		agentproto.ResultSucceeded,
	}
	for index, child := range children {
		now = time.Unix(int64(20+index*10), 0)
		agentID := "agent-" + string(rune('a'+index))
		if err := server.completeDeployment(
			ctx, child.ID, agentID,
			agentproto.ResultRequest{
				ClaimToken: tokens[index], State: states[index],
			},
		); err != nil {
			t.Fatalf("complete child: %v", err)
		}
	}
	got, err := repo.Queries.GetDeployment(ctx, parent.ID)
	if err != nil || got.Status != "failed" || got.StartedAt.Int64 != 10 ||
		got.FinishedAt.Int64 != 40 {
		t.Fatalf("parent = %#v, %v; want failed [10,40]", got, err)
	}
	if err := server.completeDeployment(
		ctx, children[0].ID, "agent-a",
		agentproto.ResultRequest{
			ClaimToken: tokens[0], State: agentproto.ResultFailed,
		},
	); !errors.Is(err, errLifecycleConflict) {
		t.Fatalf("late result error = %v", err)
	}
}
