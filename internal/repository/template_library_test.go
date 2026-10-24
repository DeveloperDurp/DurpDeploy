package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func libraryTestInput() ApplyLibraryTemplate {
	return ApplyLibraryTemplate{
		Version:        "1",
		Reviewed:       true,
		AgentSelectors: []string{"template-agent"},
		VariableValues: map[string]string{},
	}
}

func libraryTestValues(entry LibraryTemplate) map[string]string {
	values := map[string]string{
		"ARTIFACT_URL":       "https://artifacts.example.test/app",
		"ARTIFACT_TOKEN":     "fresh-secret",
		"FILE_SOURCE":        "",
		"INSTALL_PATH":       "/opt/example/app",
		"SERVICE_NAME":       "example.service",
		"HEALTH_URL":         "http://127.0.0.1:8000/health",
		"COMPOSE_FILE":       "/opt/example/compose.yml",
		"COMPOSE_PROJECT":    "example",
		"APP_IMAGE":          "example/app:1.0",
		"TF_CONFIG_DIR":      "/opt/example/terraform",
		"TF_CONTAINER_IMAGE": "example/terraform-toolchain:reviewed",
		"TF_HTTP_USERNAME":   "fixture",
		"TF_HTTP_PASSWORD":   "fresh-secret",
	}
	result := map[string]string{}
	for name := range entry.Configuration {
		result[name] = values[name]
	}
	return result
}

func TestTemplateLibraryBackendParity(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, engine string) {
		r, second := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, engine),
		)
		box, err := secret.NewBox(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		r.SetSecretBox(box)
		second.SetSecretBox(box)
		user, err := r.Queries.CreateUser(t.Context(), db.CreateUserParams{
			Name: "library", Email: "library@example.test", Role: "admin", PasswordHash: "unused",
		})
		if err != nil {
			t.Fatal(err)
		}
		actor := ProjectTemplateActor{UserID: user.ID, Role: "admin"}
		if _, err := r.Queries.AddAgentLabel(t.Context(), db.AddAgentLabelParams{
			AgentID: "race-agent", Label: "template-agent",
		}); err != nil {
			t.Fatal(err)
		}
		for _, entry := range TemplateLibrary() {
			t.Run(entry.Slug, func(t *testing.T) {
				checkLibraryAppend(t, r, actor, entry)
			})
		}
		checkLibraryConcurrency(t, r, second, actor)
		checkLibraryPermissions(t, r, actor)
	})
}

func libraryTestProject(t *testing.T, r *Repository, name string) db.Project {
	t.Helper()
	project, err := r.Queries.CreateProject(t.Context(), db.CreateProjectParams{
		Name: name, Description: sql.NullString{String: "keep metadata", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func checkLibraryAppend(
	t *testing.T,
	r *Repository,
	actor ProjectTemplateActor,
	entry LibraryTemplate,
) {
	t.Helper()
	project := libraryTestProject(t, r, entry.Slug)
	old, err := r.CreateStepWithPlacement(t.Context(), db.CreateStepParams{
		ProjectID: project.ID, Name: "Existing", ScriptBody: "echo keep",
		SortOrder: 7, Interpreter: "bash", VariableNames: "[]",
	}, "agent", []string{"template-agent"})
	if err != nil {
		t.Fatal(err)
	}
	input := libraryTestInput()
	input.VariableValues = libraryTestValues(entry)
	if !LibraryUsesAgents(entry) {
		input.AgentSelectors = nil
	}
	result, err := r.ApplyLibraryTemplate(
		t.Context(),
		actor,
		project.ID,
		entry.Slug,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.StepsCreated != len(entry.Snapshot.Steps) ||
		result.ProjectID != project.ID {
		t.Fatalf("wrong application result: %+v", result)
	}
	steps, err := r.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != len(entry.Snapshot.Steps)+1 ||
		!reflect.DeepEqual(steps[0], old) {
		t.Fatalf("existing step changed or append failed: %v, %v", steps, err)
	}
	checkLibraryCopiedSteps(t, r, steps[1:], entry, input)
	current, err := r.Queries.GetProject(t.Context(), project.ID)
	if err != nil || !reflect.DeepEqual(current, project) {
		t.Fatal("library application changed project metadata", err)
	}
	checkLibraryCopiedVariables(t, r, project.ID, entry, input)
	_, err = r.ApplyLibraryTemplate(
		t.Context(),
		actor,
		project.ID,
		entry.Slug,
		input,
	)
	if !errors.Is(err, ErrLibraryConflict) {
		t.Fatalf("repeat application must reject collisions: %v", err)
	}
	after, err := r.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || !reflect.DeepEqual(steps, after) {
		t.Fatal("rejected application changed steps", err)
	}
}

func checkLibraryCopiedSteps(
	t *testing.T,
	r *Repository,
	steps []db.Step,
	entry LibraryTemplate,
	input ApplyLibraryTemplate,
) {
	t.Helper()
	for i, s := range steps {
		want := entry.Snapshot.Steps[i]
		if s.SortOrder != int64(8+i) || s.ScriptBody != want.ScriptBody ||
			s.ExecutionTarget != want.ExecutionTarget || s.Interpreter != want.Interpreter || s.AgentExecutionMode != want.AgentExecutionMode ||
			s.ApprovalArtifactPath != want.ApprovalArtifactPath ||
			s.ApprovalReviewPath != want.ApprovalReviewPath ||
			s.ApprovalReviewFormat != want.ApprovalReviewFormat {
			t.Fatalf("step %d was not copied in order", i)
		}
		labels, err := r.Queries.ListStepAgentSelectors(t.Context(), s.ID)
		if err != nil ||
			(want.ExecutionTarget == "agent" && !reflect.DeepEqual(labels, input.AgentSelectors)) {
			t.Fatalf("lost placement: %v, %v", labels, err)
		}
		if want.ExecutionTarget == "local" &&
			(s.ContainerImage != input.VariableValues["TF_CONTAINER_IMAGE"] || len(labels) != 0) {
			t.Fatal(
				"Terraform steps lost their configured server container image",
			)
		}
	}
}

func checkLibraryCopiedVariables(
	t *testing.T,
	r *Repository,
	projectID int64,
	entry LibraryTemplate,
	input ApplyLibraryTemplate,
) {
	t.Helper()
	variables, err := r.Queries.ListVariablesByProject(t.Context(), projectID)
	if err != nil || len(variables) != len(entry.Snapshot.Variables) {
		t.Fatalf("variables not copied: %v", err)
	}
	for _, variable := range variables {
		plain, err := r.decryptVariable(variable)
		if err != nil ||
			plain.Value.String != input.VariableValues[variable.Name] {
			t.Fatal("configured value was not preserved", err)
		}
		if variable.Secret == 1 &&
			strings.Contains(variable.Value.String, "fresh-secret") {
			t.Fatal("secret was stored in plaintext")
		}
	}
}

func checkLibraryConcurrency(
	t *testing.T,
	first, second *Repository,
	actor ProjectTemplateActor,
) {
	t.Helper()
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent-%t", same), func(t *testing.T) {
			checkLibraryConcurrentApplication(t, first, second, actor, same)
		})
	}
}

func checkLibraryConcurrentApplication(
	t *testing.T,
	first, second *Repository,
	actor ProjectTemplateActor,
	same bool,
) {
	t.Helper()
	project := libraryTestProject(
		t,
		first,
		fmt.Sprintf("concurrent-%t", same),
	)
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var workers sync.WaitGroup
	for i, repo := range []*Repository{first, second} {
		slug := "http-health-check"
		if i == 1 && !same {
			slug = "service-restart"
		}
		entry, err := GetLibraryTemplate(slug)
		if err != nil {
			t.Fatal(err)
		}
		input := libraryTestInput()
		input.VariableValues = libraryTestValues(entry)
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := repo.ApplyLibraryTemplate(
				t.Context(),
				actor,
				project.ID,
				entry.Slug,
				input,
			)
			errorsOut <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errorsOut)
	conflicts := libraryConflictCount(t, errorsOut)
	want := 2
	if same {
		want = 1
	}
	steps, err := first.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != want || conflicts != 2-want {
		t.Fatalf(
			"concurrent application lost atomicity: steps=%d conflicts=%d err=%v",
			len(steps),
			conflicts,
			err,
		)
	}
	for i, step := range steps {
		if step.SortOrder != int64(i+1) {
			t.Fatal(
				"concurrent applications produced duplicate order values",
			)
		}
	}
}

func libraryConflictCount(t *testing.T, errorsOut <-chan error) int {
	t.Helper()
	conflicts := 0
	for err := range errorsOut {
		if errors.Is(err, ErrLibraryConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	return conflicts
}

func checkLibraryPermissions(
	t *testing.T,
	r *Repository,
	actor ProjectTemplateActor,
) {
	t.Helper()
	project := libraryTestProject(t, r, "permission-rejections")
	entry, err := GetLibraryTemplate("http-health-check")
	if err != nil {
		t.Fatal(err)
	}
	input := libraryTestInput()
	input.VariableValues = libraryTestValues(entry)
	for _, role := range []string{"viewer", "deployer"} {
		restricted := actor
		restricted.Role = role
		_, err := r.ApplyLibraryTemplate(
			t.Context(),
			restricted,
			project.ID,
			entry.Slug,
			input,
		)
		if !errors.Is(err, ErrProjectTemplateAccess) {
			t.Fatalf("%s applied a template: %v", role, err)
		}
	}
	steps, err := r.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatal("permission rejection left partial configuration", err)
	}
}

func TestTemplateLibraryRollback(t *testing.T) {
	f := newProjectTemplateBackendFixture(t, "SQLite")
	project := libraryTestProject(t, f.r, "rollback")
	if _, err := f.r.DB.ExecContext(t.Context(), `CREATE TRIGGER reject_library_step
		BEFORE INSERT ON steps BEGIN SELECT RAISE(ABORT, 'injected step failure'); END`); err != nil {
		t.Fatal(err)
	}
	entry, err := GetLibraryTemplate("linux-service")
	if err != nil {
		t.Fatal(err)
	}
	input := libraryTestInput()
	input.VariableValues = libraryTestValues(entry)
	if _, err := f.r.ApplyLibraryTemplate(t.Context(), f.actor, project.ID, entry.Slug, input); err == nil {
		t.Fatal("injected storage failure was ignored")
	}
	variables, err := f.r.Queries.ListVariablesByProject(
		t.Context(),
		project.ID,
	)
	if err != nil || len(variables) != 0 {
		t.Fatal("storage failure left partial variables", err)
	}
	steps, err := f.r.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatal("storage failure left partial steps", err)
	}
}
