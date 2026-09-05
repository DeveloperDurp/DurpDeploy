package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

func TestDeploymentExecutionTarget_HTMLFormsFreezeIdenticalIntent(
	t *testing.T,
) {
	// Given
	repo := repository.New(newHandlerTestDatabase(t))
	project, err := repo.Queries.CreateProject(
		context.Background(), db.CreateProjectParams{Name: "html-targets"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		context.Background(), db.CreateEnvironmentParams{Name: "html-env"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(
		context.Background(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deploymentHandler := handler.NewDeploymentHandler(
		repo, nil, dispatch.New(repo, nil, nil),
	)

	for _, surface := range []string{"dedicated form", "release quick form"} {
		for _, mode := range []string{"default", "local"} {
			t.Run(surface+" "+mode, func(t *testing.T) {
				form := url.Values{
					"release_id":     {fmt.Sprint(release.ID)},
					"environment_id": {fmt.Sprint(environment.ID)},
					"target_mode":    {mode},
					"agent_strategy": {"round_robin"},
				}
				request := httptest.NewRequest(
					http.MethodPost, "/projects/1/deploy",
					strings.NewReader(form.Encode()),
				)
				request.Header.Set(
					"Content-Type",
					"application/x-www-form-urlencoded",
				)
				request = auth.SetUser(
					request,
					&db.User{ID: 1, Name: "admin", Role: "admin"},
				)
				routeContext := chi.NewRouteContext()
				routeContext.URLParams.Add("id", fmt.Sprint(project.ID))
				request = request.WithContext(context.WithValue(
					request.Context(), chi.RouteCtxKey, routeContext,
				))
				recorder := httptest.NewRecorder()

				// When
				deploymentHandler.ScheduleDeployment(recorder, request)

				// Then
				if recorder.Code != http.StatusSeeOther {
					t.Fatalf(
						"status = %d, body = %s",
						recorder.Code,
						recorder.Body,
					)
				}
				deploymentID, err := strconv.ParseInt(
					strings.TrimPrefix(
						recorder.Header().Get("Location"),
						"/deployments/",
					),
					10,
					64,
				)
				if err != nil {
					t.Fatalf("parse deployment location: %v", err)
				}
				snapshot, err := repo.Queries.GetDeploymentRoutingSnapshot(
					context.Background(), deploymentID,
				)
				if err != nil {
					t.Fatalf("get routing snapshot: %v", err)
				}
				wantSource := "request"
				if mode == "default" {
					wantSource = "legacy"
				}
				if snapshot.Source != wantSource ||
					snapshot.TargetMode != "local" {
					t.Fatalf("snapshot = %#v", snapshot)
				}
			})
		}
	}
}
