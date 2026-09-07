package agentserver

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/repository"

	"github.com/go-chi/chi/v5"
)

func (f *labelRuntimeFixture) requireApproval(t *testing.T) {
	t.Helper()
	lifecycle, err := f.repo.Queries.CreateLifecycle(t.Context(),
		db.CreateLifecycleParams{Name: f.project.Name})
	runtimeMust(t, err)
	runtimeMust(t, f.repo.Queries.SetProjectLifecycle(t.Context(),
		db.SetProjectLifecycleParams{ID: f.project.ID,
			LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true}}))
	_, err = f.repo.Queries.CreateLifecycleStage(t.Context(),
		db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: f.environment.ID,
			RequiresApproval: 1,
		})
	runtimeMust(t, err)
}

func (f *labelRuntimeFixture) approvalServer(
	t *testing.T, repo *repository.Repository,
) *httptest.Server {
	t.Helper()
	handler := api.NewDeploymentHandler(
		repo,
		nil,
		dispatch.New(repo, f.box, nil),
	)
	router := chi.NewRouter()
	router.Post(
		"/api/v1/deployments/{id}/approve",
		func(w http.ResponseWriter, r *http.Request) {
			handler.ApproveDeployment(w, auth.SetUser(r,
				&db.User{Name: "runtime-admin", Role: "admin"}))
		},
	)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

type runtimeHTTPResult struct {
	status int
	body   string
	err    error
}

func runtimeApprove(url string) runtimeHTTPResult {
	client := http.Client{Timeout: 30 * time.Second}
	response, err := client.Post(url, "application/json", nil)
	if err != nil {
		return runtimeHTTPResult{err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	return runtimeHTTPResult{
		status: response.StatusCode,
		body:   string(body),
		err:    err,
	}
}

func runtimeApprovalCAS(t *testing.T, f *labelRuntimeFixture) {
	f.requireApproval(t)
	root := f.create(t, "all")
	f.approvalCAS(t, root.ID)
	f.assertAgents(t, root.ID, f.agents)
	if got := f.routingCounts(t); !reflect.DeepEqual(
		got,
		[]int{4, 1, 3, 3, 1, 0},
	) {
		t.Fatalf("approval counts=%v", got)
	}
}

func (f *labelRuntimeFixture) approvalCAS(t *testing.T, id int64) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan runtimeHTTPResult, 2)
	for range 2 {
		server := f.approvalServer(t, f.connection(t))
		go func() {
			<-start
			results <- runtimeApprove(server.URL + "/api/v1/deployments/" +
				strconv.FormatInt(id, 10) + "/approve")
		}()
	}
	close(start)
	var statuses []int
	for range 2 {
		result := <-results
		runtimeMust(t, result.err)
		t.Logf(
			"HTTP POST approve status=%d body=%s",
			result.status,
			result.body,
		)
		statuses = append(statuses, result.status)
	}
	sort.Ints(statuses)
	if !reflect.DeepEqual(statuses, []int{http.StatusOK, http.StatusConflict}) {
		t.Fatalf("concurrent approval statuses=%v", statuses)
	}
}

func runtimeApprovalRollback(t *testing.T, f *labelRuntimeFixture) {
	f.requireApproval(t)
	root := f.create(t, "round_robin")
	before := f.routingCounts(t)
	snapshot, err := f.repo.Queries.GetDeploymentRoutingSnapshot(
		t.Context(),
		root.ID,
	)
	runtimeMust(t, err)
	for _, id := range f.agents {
		_, err := f.repo.DB.ExecContext(t.Context(),
			"UPDATE agents SET status='disabled' WHERE id=?", id)
		runtimeMust(t, err)
	}
	server := f.approvalServer(t, f.connection(t))
	response := runtimeApprove(server.URL + "/api/v1/deployments/" +
		strconv.FormatInt(root.ID, 10) + "/approve")
	runtimeMust(t, response.err)
	t.Logf("HTTP unavailable approval status=%d", response.status)
	if response.status != http.StatusConflict {
		t.Fatalf("approval status=%d body=%s", response.status, response.body)
	}
	stored, err := f.repo.Queries.GetDeployment(t.Context(), root.ID)
	runtimeMust(t, err)
	afterSnapshot, err := f.repo.Queries.GetDeploymentRoutingSnapshot(
		t.Context(),
		root.ID,
	)
	runtimeMust(t, err)
	if stored != root || snapshot != afterSnapshot ||
		!reflect.DeepEqual(before, f.routingCounts(t)) {
		t.Fatal(
			"unavailable approval mutated pending root or routing artifacts",
		)
	}
	f.addAgent(t, f.project.Name+"-new")
	response = runtimeApprove(server.URL + "/api/v1/deployments/" +
		strconv.FormatInt(root.ID, 10) + "/approve")
	runtimeMust(t, response.err)
	if response.status != http.StatusOK {
		t.Fatalf(
			"later eligible approval=%d body=%s",
			response.status,
			response.body,
		)
	}
	f.assertAgents(t, root.ID, []string{f.project.Name + "-new"})
}

func (f *labelRuntimeFixture) assertAgents(
	t *testing.T,
	id int64,
	want []string,
) {
	t.Helper()
	rows, err := f.repo.Queries.ListDeploymentRoutingAgents(t.Context(), id)
	runtimeMust(t, err)
	var got []string
	for i, row := range rows {
		if row.Position != int64(i) {
			t.Fatalf("routing position=%d want=%d", row.Position, i)
		}
		got = append(got, row.AgentID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered agents=%v want=%v", got, want)
	}
	t.Logf("immutable_ordered_agents=%v", got)
}
