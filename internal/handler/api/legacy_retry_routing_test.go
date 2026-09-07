package api_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/handler/api"
)

func TestLegacyLocalRouting_RetryPreservesAndRedeployResolves(t *testing.T) {
	for _, retry := range []bool{true, false} {
		name, want := "redeploy", "remote"
		if retry {
			name, want = "retry", "local"
		}
		t.Run(name, func(t *testing.T) {
			// Given a migrated local deployment without a routing snapshot,
			// and a current environment assignment to retry-agent.
			h, box, source := retryFixture(t, "failed")
			ctx := context.Background()
			_, err := h.repo.Queries.CreateDeploymentDispatch(ctx,
				db.CreateDeploymentDispatchParams{
					DeploymentID: source.ID, Mode: "local", State: "failed",
				})
			if err != nil {
				t.Fatal(err)
			}
			_, err = h.repo.Queries.GetDeploymentRoutingSnapshot(ctx, source.ID)
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("legacy snapshot: %v", err)
			}
			handler := api.NewDeploymentHandler(h.repo, nil,
				dispatch.New(h.repo, box, nil))
			var action http.HandlerFunc = handler.RedeployDeployment
			if retry {
				action = handler.RetryDeployment
			}

			// When the API creates a retry or redeployment.
			created := invokeDeploymentAction(t, action, source.ID)

			// Then the persisted dispatch reflects that action's policy.
			actual, err := h.repo.Queries.GetDeploymentDispatch(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: HTTP 201; dispatch=%s assigned=%q",
				name, actual.Mode, actual.AssignedAgentID.String)
			if actual.Mode != want {
				t.Fatalf("dispatch mode = %q, want %q", actual.Mode, want)
			}
			if retry && actual.AssignedAgentID.Valid {
				t.Fatalf("local retry assigned agent = %q",
					actual.AssignedAgentID.String)
			}
			if !retry && actual.AssignedAgentID.String != "retry-agent" {
				t.Fatalf("redeploy assigned agent = %q",
					actual.AssignedAgentID.String)
			}
		})
	}
}
