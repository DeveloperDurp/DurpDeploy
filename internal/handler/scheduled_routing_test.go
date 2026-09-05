package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

func TestScheduledExecutionTarget_HTMLCreatePersistsExplicitPolicy(t *testing.T) {
	// Given
	repo := repository.New(newHandlerTestDatabase(t))
	project, err := repo.Queries.CreateProject(
		context.Background(), db.CreateProjectParams{Name: "schedule-routing"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		context.Background(), db.CreateEnvironmentParams{Name: "production"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(
		context.Background(), db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	label, err := repo.Queries.CreateAgentLabel(
		context.Background(), db.CreateAgentLabelParams{
			Name: "Builders", NormalizedName: "builders",
		},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	form := url.Values{
		"release_id":     {fmt.Sprint(release.ID)},
		"environment_id": {fmt.Sprint(environment.ID)},
		"cron":           {"0 0 * * *"},
		"target_mode":    {"label"},
		"agent_label_id": {fmt.Sprint(label.ID)},
		"agent_strategy": {"all"},
	}
	request := httptest.NewRequest(
		http.MethodPost, "/", strings.NewReader(form.Encode()),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	route := chi.NewRouteContext()
	route.URLParams.Add("id", fmt.Sprint(project.ID))
	request = request.WithContext(context.WithValue(
		request.Context(), chi.RouteCtxKey, route,
	))
	recorder := httptest.NewRecorder()
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)

	// When
	handler.NewScheduledDeploymentHandler(repo, parser).Create(recorder, request)

	// Then
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", recorder.Code, recorder.Body.String())
	}
	schedules, err := repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(), project.ID,
	)
	if err != nil || len(schedules) != 1 {
		t.Fatalf("schedules = %d, error = %v", len(schedules), err)
	}
	policy, err := repo.Queries.GetScheduledDeploymentRoutingPolicy(
		context.Background(), schedules[0].ID,
	)
	if err != nil || policy.TargetMode != "label" ||
		policy.AgentLabelID.Int64 != label.ID || policy.AgentStrategy.String != "all" {
		t.Fatalf("policy = %#v, error = %v", policy, err)
	}
}
