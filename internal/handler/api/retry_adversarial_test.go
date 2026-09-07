package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestRetryRoutingSnapshot_InactiveOrDeletedTargetCreatesNoRoot(t *testing.T) {
	for _, test := range []struct {
		name   string
		remove func(*testing.T, *exactRetrySetup)
	}{
		{
			name: "inactive",
			remove: func(t *testing.T, setup *exactRetrySetup) {
				_, err := setup.h.repo.DB.ExecContext(
					setup.ctx,
					"UPDATE agents SET status = 'disabled' WHERE id = 'agent-b'",
				)
				if err != nil {
					t.Fatalf("disable exact target: %v", err)
				}
			},
		},
		{
			name: "deleted",
			remove: func(t *testing.T, setup *exactRetrySetup) {
				if _, err := setup.h.repo.Queries.DeleteAgentLabelMembership(
					setup.ctx, db.DeleteAgentLabelMembershipParams{
						AgentLabelID: setup.label.ID, AgentID: "agent-b",
					},
				); err != nil {
					t.Fatalf("delete membership: %v", err)
				}
				if _, err := setup.h.repo.Queries.DeleteAgentPairing(
					setup.ctx, "agent-b",
				); err != nil {
					t.Fatalf("delete pairing: %v", err)
				}
				if _, err := setup.h.repo.Queries.DeleteAgent(
					setup.ctx, "agent-b",
				); err != nil {
					t.Fatalf("delete agent: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given
			setup := newExactRetrySetup(t, false)
			test.remove(t, setup)
			handler := api.NewDeploymentHandler(
				setup.h.repo, nil,
				dispatch.New(setup.h.repo, setup.box, nil),
			)
			recorder := httptest.NewRecorder()
			request := withAPIURLParam(
				httptest.NewRequest(http.MethodPost, "/", nil),
				"id", fmt.Sprint(setup.source.ID),
			)

			// When
			handler.RetryDeployment(recorder, request)

			// Then
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body)
			}
			deployments, err := setup.h.repo.Queries.ListDeploymentsByRelease(
				setup.ctx, setup.source.ReleaseID,
			)
			if err != nil || len(deployments) != 1 {
				t.Fatalf("deployments = %#v, error = %v", deployments, err)
			}
		})
	}
}

func TestRetryApprovalExactSet_SeparateConnectionsHaveOneWinner(t *testing.T) {
	// Given
	setup := newExactRetrySetup(t, true)
	service := dispatch.NewCreationService(
		setup.h.repo, dispatch.New(setup.h.repo, setup.box, nil),
	)
	retry, err := service.Retry(setup.ctx, setup.source)
	if err != nil {
		t.Fatalf("create approval retry: %v", err)
	}
	secondDB, err := sql.Open("sqlite", setup.h.dsn)
	if err != nil {
		t.Fatalf("open second database: %v", err)
	}
	t.Cleanup(func() { _ = secondDB.Close() })
	secondRepo := repository.New(secondDB)
	user := seedAPIUser(t, setup.h.repo, "approver@example.com", "admin")
	handlers := []*api.DeploymentHandler{
		api.NewDeploymentHandler(
			setup.h.repo, nil, dispatch.New(setup.h.repo, setup.box, nil),
		),
		api.NewDeploymentHandler(
			secondRepo, nil, dispatch.New(secondRepo, setup.box, nil),
		),
	}

	// When
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for _, handler := range handlers {
		go func() {
			defer wait.Done()
			<-start
			recorder := httptest.NewRecorder()
			request := withAPIUser(
				withAPIURLParam(
					httptest.NewRequest(http.MethodPost, "/", nil),
					"id", fmt.Sprint(retry.ID),
				),
				user,
			)
			handler.ApproveDeployment(recorder, request)
			statuses <- recorder.Code
		}()
	}
	close(start)
	wait.Wait()
	close(statuses)

	// Then
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("approval statuses = %v, want one 200 and one 409", counts)
	}
	assertRetryApprovalSideEffects(t, setup, retry.ID)
}

type exactRetrySetup struct {
	h      *harness
	ctx    context.Context
	box    *secret.Box
	source db.Deployment
	label  db.AgentLabel
}

func newExactRetrySetup(t *testing.T, approval bool) *exactRetrySetup {
	t.Helper()
	h := newAPIHarness(t)
	ctx := context.Background()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	release, err := h.repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "exact", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	if approval {
		seedRetryApprovalStage(t, h, project.ID, environment.ID)
	}
	label, err := h.repo.Queries.CreateAgentLabel(
		ctx, db.CreateAgentLabelParams{
			Name: "Exact", NormalizedName: "exact",
		},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		createRetryEligibleAgent(t, h, label.ID, id)
	}
	source := seedDeployment(t, h.repo, release.ID, environment.ID, "failed")
	if _, err := h.repo.Queries.CreateDeploymentRoutingSnapshot(
		ctx, db.CreateDeploymentRoutingSnapshotParams{
			DeploymentID: source.ID, Source: "request", TargetMode: "label",
			AgentLabelID:   sql.NullInt64{Int64: label.ID, Valid: true},
			AgentLabelName: sql.NullString{String: label.Name, Valid: true},
			AgentStrategy:  sql.NullString{String: "all", Valid: true},
		},
	); err != nil {
		t.Fatalf("create source snapshot: %v", err)
	}
	for position, id := range []string{"agent-b", "agent-a"} {
		if _, err := h.repo.Queries.AddDeploymentRoutingAgent(
			ctx, db.AddDeploymentRoutingAgentParams{
				DeploymentID: source.ID, Position: int64(position),
				AgentID: id, AgentName: id,
			},
		); err != nil {
			t.Fatalf("add exact target: %v", err)
		}
	}
	return &exactRetrySetup{
		h: h, ctx: ctx, box: box, source: source, label: label,
	}
}

func seedRetryApprovalStage(
	t *testing.T,
	h *harness,
	projectID int64,
	environmentID int64,
) {
	t.Helper()
	lifecycle, err := h.repo.Queries.CreateLifecycle(
		context.Background(), db.CreateLifecycleParams{Name: "exact-approval"},
	)
	if err != nil {
		t.Fatalf("create lifecycle: %v", err)
	}
	if err := h.repo.Queries.SetProjectLifecycle(
		context.Background(), db.SetProjectLifecycleParams{
			ID:          projectID,
			LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true},
		},
	); err != nil {
		t.Fatalf("set lifecycle: %v", err)
	}
	if _, err := h.repo.Queries.CreateLifecycleStage(
		context.Background(), db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: environmentID,
			RequiresApproval: 1,
		},
	); err != nil {
		t.Fatalf("create approval stage: %v", err)
	}
}

func assertRetryApprovalSideEffects(
	t *testing.T,
	setup *exactRetrySetup,
	retryID int64,
) {
	t.Helper()
	checks := []struct {
		name string
		want int
		sql  string
	}{
		{"approval", 1, "SELECT COUNT(*) FROM deployment_approvals WHERE deployment_id = ?"},
		{"exact agents", 2, "SELECT COUNT(*) FROM deployment_routing_agents WHERE deployment_id = ?"},
		{"children", 2, "SELECT COUNT(*) FROM deployments WHERE parent_deployment_id = ?"},
		{
			"dispatches", 2,
			`SELECT COUNT(*)
FROM deployment_dispatches AS dispatch
JOIN deployments AS child ON child.id = dispatch.deployment_id
WHERE child.parent_deployment_id = ?`,
		},
	}
	for _, check := range checks {
		var count int
		if err := setup.h.repo.DB.QueryRowContext(
			setup.ctx, check.sql, retryID,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if count != check.want {
			t.Fatalf("%s count = %d, want %d", check.name, count, check.want)
		}
	}
}
