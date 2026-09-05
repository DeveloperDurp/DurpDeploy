package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

func TestProjectExecutionPolicy_HandlerPersistenceUsesTypedResolver(
	t *testing.T,
) {
	ctx := context.Background()
	repo := repository.New(newHandlerTestDatabase(t))
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name:        "handler-policy",
			Description: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	label, err := repo.Queries.CreateAgentLabel(
		ctx,
		db.CreateAgentLabelParams{Name: "Cat Fact", NormalizedName: "cat fact"},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	policy, err := handler.ResolveProjectExecutionPolicy(
		ctx,
		repo,
		handler.ProjectExecutionPolicyInput{
			TargetMode: "label", LabelID: label.ID, Strategy: "round_robin",
		},
	)
	if err != nil {
		t.Fatalf("resolve typed policy: %v", err)
	}
	if err := handler.SaveProjectExecutionPolicy(
		ctx,
		repo.Queries,
		project.ID,
		policy,
	); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	stored, err := handler.LoadProjectExecutionPolicy(
		ctx,
		repo.Queries,
		project.ID,
	)
	if err != nil || stored.LabelName != "Cat Fact" ||
		stored.Strategy != "round_robin" {
		t.Fatalf("stored policy = %#v, %v", stored, err)
	}
	t.Logf(
		"SQLite policy: project=%d mode=%s label=%q strategy=%s",
		project.ID,
		stored.TargetMode,
		stored.LabelName,
		stored.Strategy,
	)
}

func TestProjectExecutionPolicy_ValidationPreservesSubmittedProjectFields(
	t *testing.T,
) {
	ctx := context.Background()
	repo := repository.New(newHandlerTestDatabase(t))
	projectHandler := handler.NewProjectHandler(repo)
	lifecycle, err := repo.Queries.CreateLifecycle(
		ctx,
		db.CreateLifecycleParams{
			Name:        "Approval",
			Description: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create lifecycle: %v", err)
	}
	label, err := repo.Queries.CreateAgentLabel(
		ctx,
		db.CreateAgentLabelParams{Name: "Cat Fact", NormalizedName: "cat fact"},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	for _, scenario := range []struct {
		name    string
		project db.Project
		invoke  func(http.ResponseWriter, *http.Request)
	}{
		{name: "create", invoke: projectHandler.CreateProject},
		{
			name:    "edit",
			project: createProjectForValidationTest(t, ctx, repo, "existing-project"),
			invoke:  projectHandler.UpdateProject,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			form := url.Values{
				"name":           {"submitted-" + scenario.name},
				"description":    {"submitted description " + scenario.name},
				"lifecycle_id":   {strconv.FormatInt(lifecycle.ID, 10)},
				"target_mode":    {"label"},
				"agent_label_id": {strconv.FormatInt(label.ID, 10)},
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/projects",
				strings.NewReader(form.Encode()),
			)
			request.Header.Set(
				"Content-Type",
				"application/x-www-form-urlencoded",
			)
			request.Header.Set("HX-Request", "true")
			if scenario.project.ID != 0 {
				routeContext := chi.NewRouteContext()
				routeContext.URLParams.Add(
					"id",
					strconv.FormatInt(scenario.project.ID, 10),
				)
				request = request.WithContext(context.WithValue(
					request.Context(),
					chi.RouteCtxKey,
					routeContext,
				))
			}
			recorder := httptest.NewRecorder()
			scenario.invoke(recorder, request)
			if recorder.Code != http.StatusUnprocessableEntity {
				t.Fatalf(
					"status = %d, want 422: %s",
					recorder.Code,
					recorder.Body.String(),
				)
			}
			for _, expected := range []string{
				`value="submitted-` + scenario.name + `"`,
				`>submitted description ` + scenario.name + `</textarea>`,
				`value="` + strconv.FormatInt(lifecycle.ID, 10) + `" selected`,
				`name="target_mode" value="label" class="radio radio-primary" checked`,
				`option value="` + strconv.FormatInt(label.ID, 10) + `" selected`,
			} {
				if !strings.Contains(recorder.Body.String(), expected) {
					t.Errorf(
						"validation response missing %q: %s",
						expected,
						recorder.Body.String(),
					)
				}
			}
		})
	}
}

func createProjectForValidationTest(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
	name string,
) db.Project {
	t.Helper()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: name, Description: sql.NullString{}},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}
