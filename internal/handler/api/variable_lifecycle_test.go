package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
)

func TestVariableAPI_LifecycleScopesOnCreateAndUpdate(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "variable-lifecycle@example.com", "admin")
	token := h.seedToken(t, admin)
	project := h.seedProject(t, admin)
	allowed, outside := seedVariableLifecycle(t, h, project.ID)
	ctx := context.Background()
	path := "/api/v1/projects/" + itoa(project.ID) + "/variables"
	create := func(scope int64) int {
		t.Helper()
		return h.request(
			t,
			http.MethodPost,
			path,
			token,
			fmt.Sprintf(
				`{"name":"KEY","value":"value","environment_id":%d}`,
				scope,
			),
		).Code
	}
	if status := create(outside.ID); status != http.StatusUnprocessableEntity {
		t.Fatalf("outside create = %d", status)
	}
	if status := create(allowed.ID); status != http.StatusCreated {
		t.Fatalf("allowed create = %d", status)
	}
	variables, err := h.repo.Queries.ListVariablesByProject(ctx, project.ID)
	if err != nil || len(variables) != 1 {
		t.Fatalf("created variables = %v, %v", variables, err)
	}
	updatePath := path + "/" + itoa(variables[0].ID)
	update := func(scope int64) int {
		t.Helper()
		return h.request(
			t,
			http.MethodPut,
			updatePath,
			token,
			fmt.Sprintf(
				`{"name":"KEY","value":"new","environment_id":%d}`,
				scope,
			),
		).Code
	}
	if status := update(outside.ID); status != http.StatusUnprocessableEntity {
		t.Fatalf("outside update = %d", status)
	}
	if status := h.request(t, http.MethodPut, updatePath, token,
		`{"name":"KEY","environment_id":null}`).Code; status != http.StatusOK {
		t.Fatalf("unscoped update = %d", status)
	}
	if status := update(allowed.ID); status != http.StatusOK {
		t.Fatalf("allowed update = %d", status)
	}
	unbound, err := h.repo.Queries.CreateProject(ctx,
		db.CreateProjectParams{Name: "unbound-variable-api"})
	if err != nil {
		t.Fatal(err)
	}
	if status := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(unbound.ID)+"/variables",
		token,
		fmt.Sprintf(
			`{"name":"KEY","environment_id":%d}`,
			outside.ID,
		),
	).Code; status != http.StatusCreated {
		t.Fatalf("unbound create = %d", status)
	}
	unboundVariables, err := h.repo.Queries.ListVariablesByProject(
		ctx,
		unbound.ID,
	)
	if err != nil || len(unboundVariables) != 1 {
		t.Fatalf("unbound variables = %v, %v", unboundVariables, err)
	}
	if status := h.request(
		t,
		http.MethodPut,
		"/api/v1/projects/"+itoa(unbound.ID)+"/variables/"+
			itoa(unboundVariables[0].ID),
		token,
		fmt.Sprintf(
			`{"name":"KEY2","environment_id":%d}`,
			outside.ID,
		),
	).Code; status != http.StatusOK {
		t.Fatalf("unbound update = %d", status)
	}
	if status := h.request(
		t,
		http.MethodPost,
		path,
		token,
		`{"name":"UNSCOPED","environment_id":null}`,
	).Code; status != http.StatusCreated {
		t.Fatalf("unscoped create = %d", status)
	}
}

func seedVariableLifecycle(
	t *testing.T,
	h *testHarness,
	projectID int64,
) (db.Environment, db.Environment) {
	t.Helper()
	ctx := t.Context()
	allowed, err := h.repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "api-allowed-variable-env"})
	if err != nil {
		t.Fatal(err)
	}
	outside, err := h.repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "api-outside-variable-env"})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := h.repo.Queries.CreateLifecycle(ctx,
		db.CreateLifecycleParams{Name: "api-variable-lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateLifecycleStage(ctx,
		db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: allowed.ID, SortOrder: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.repo.Queries.SetProjectLifecycle(ctx,
		db.SetProjectLifecycleParams{
			ID:          projectID,
			LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true},
		}); err != nil {
		t.Fatal(err)
	}
	return allowed, outside
}
