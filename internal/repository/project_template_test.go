package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func TestProjectTemplateBackendParity(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, engine string) {
		f := newProjectTemplateBackendFixture(t, engine)
		f.checkRejectedApplies(t)
		created := f.checkCreatedProject(t)
		f.checkIndependence(t, created.ID)
		f.checkAccessAndDeletion(t)
	})
}

type projectTemplateBackendFixture struct {
	r        *Repository
	actor    ProjectTemplateActor
	template db.ProjectTemplate
	snapshot ProjectTemplateSnapshot
	input    ApplyProjectTemplate
}

func newProjectTemplateBackendFixture(
	t *testing.T,
	engine string,
) projectTemplateBackendFixture {
	t.Helper()
	r, _ := openDeploymentCreationEngine(
		t,
		newDeploymentCreationEngine(t, engine),
	)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	r.SetSecretBox(box)
	user, err := r.Queries.CreateUser(
		t.Context(),
		db.CreateUserParams{
			Name:         "author",
			Email:        "author@example.test",
			PasswordHash: "unused",
			Role:         "admin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	actor := ProjectTemplateActor{UserID: user.ID, Role: "admin"}
	seedProjectTemplateBackend(t, r)
	template, err := r.SaveProjectTemplate(
		t.Context(), actor, 1, "Reusable", "template description",
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ReadProjectTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "never-copy") {
		t.Fatal("secret value or package credential entered snapshot")
	}
	if snapshot.PackageRepository == nil ||
		snapshot.PackageRepository.Credential != "" {
		t.Fatal("package definition missing or credential copied")
	}
	input := ApplyProjectTemplate{
		Name: "independent", Description: &snapshot.Description, Reviewed: true,
		EnvironmentMappings: map[string]int64{"1": 1},
		VariableValues:      map[string]string{},
		PackageRepository: &ProjectTemplatePackage{
			URLTemplate: snapshot.PackageRepository.URLTemplate,
			AuthType:    "bearer", Credential: "fresh-package-credential",
		},
	}
	for _, v := range snapshot.Variables {
		if v.Secret {
			input.VariableValues[v.Key] = "fresh-secret-value"
		}
	}
	return projectTemplateBackendFixture{r, actor, template, snapshot, input}
}

func seedProjectTemplateBackend(t *testing.T, r *Repository) {
	t.Helper()
	for _, v := range []db.CreateVariableParams{
		{ProjectID: 1, Name: "DEFAULT_VALUE", Value: sql.NullString{String: "copied-default", Valid: true}},
		{ProjectID: 1, Name: "SECRET_VALUE", Value: sql.NullString{String: "never-copy-this-secret", Valid: true}, Secret: 1, IsCredential: 1, EnvironmentID: sql.NullInt64{Int64: 1, Valid: true}},
	} {
		if _, err := r.CreateVariable(t.Context(), v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Queries.AddAgentLabel(
		t.Context(),
		db.AddAgentLabelParams{
			AgentID: "race-agent",
			Label:   "template-agent",
		},
	); err != nil {
		t.Fatal(err)
	}
	seedProjectTemplateBackendSteps(t, r)
	if _, err := r.SaveProjectPackageRepository(
		t.Context(), 1,
		artifact.Repository{
			URLTemplate: "https://packages.example.test/{version}/app.zip",
			AuthType:    "bearer", Credential: "never-copy-package-credential",
		},
	); err != nil {
		t.Fatal(err)
	}
}

func seedProjectTemplateBackendSteps(t *testing.T, r *Repository) {
	t.Helper()
	for i, target := range []string{"local", "agent", "agent"} {
		network := ""
		if target == "local" {
			network = "bridge"
		}
		mode, image := "host", "harbor.durp.info/dockerhub/library/bash:5.2"
		if i == 1 {
			image = ""
		}
		if i == 2 {
			mode = "container"
		}
		if _, err := r.CreateStepWithPlacement(
			t.Context(),
			db.CreateStepParams{
				ProjectID:          1,
				Name:               target + mode,
				ScriptBody:         "echo template-original",
				SortOrder:          int64(i + 1),
				Interpreter:        "bash",
				TimeoutSeconds:     57,
				MaxRetries:         2,
				AgentExecutionMode: mode,
				ContainerImage:     image,
				VariableNames:      `["DEFAULT_VALUE"]`,
				NetworkMode:        network,
			},
			target,
			[]string{"template-agent"},
		); err != nil {
			t.Fatal(err)
		}
	}
}

func (f projectTemplateBackendFixture) checkRejectedApplies(t *testing.T) {
	t.Helper()
	r, actor, template, snapshot, input := f.r, f.actor, f.template, f.snapshot, f.input
	before, err := r.Queries.CountProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bad := input
	bad.AgentSelectors = map[string][]string{"2": {"missing-label"}}
	if _, err := r.CreateProjectFromTemplate(
		t.Context(),
		actor,
		1,
		template.ID,
		bad,
	); !errors.Is(
		err,
		ErrProjectTemplateInvalid,
	) {
		t.Fatalf("invalid selector: %v", err)
	}
	bad = input
	bad.PackageRepository = &ProjectTemplatePackage{
		URLTemplate: snapshot.PackageRepository.URLTemplate,
		AuthType:    "bearer",
	}
	if _, err := r.CreateProjectFromTemplate(
		t.Context(),
		actor,
		1,
		template.ID,
		bad,
	); !errors.Is(
		err,
		ErrProjectTemplateInvalid,
	) {
		t.Fatalf("missing credential: %v", err)
	}
	after, err := r.Queries.CountProjects(t.Context())
	if err != nil || before != after {
		t.Fatalf(
			"failed apply left a partial project: %d -> %d, %v",
			before,
			after,
			err,
		)
	}
}

func (f projectTemplateBackendFixture) checkCreatedProject(
	t *testing.T,
) db.Project {
	t.Helper()
	r, actor, template, snapshot, input := f.r, f.actor, f.template, f.snapshot, f.input
	created, err := r.CreateProjectFromTemplate(
		t.Context(),
		actor,
		1,
		template.ID,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	copiedSnapshot, err := r.CaptureProjectTemplate(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Steps, copiedSnapshot.Steps) {
		t.Fatalf(
			"step configuration changed: %#v != %#v",
			snapshot.Steps,
			copiedSnapshot.Steps,
		)
	}
	values, err := r.ListVariablesByProject(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.Name == "SECRET_VALUE" && v.IsCredential != 1 {
			t.Fatal("credential classification was not copied")
		}
		if v.Secret != 0 && v.Value.String != "fresh-secret-value" {
			t.Fatal("secret was not replaced")
		}
		if v.Secret == 0 && v.Value.String != "copied-default" {
			t.Fatal("non-secret default was not copied")
		}
	}
	member, err := r.Queries.GetProjectMember(
		t.Context(),
		db.GetProjectMemberParams{ProjectID: created.ID, UserID: actor.UserID},
	)
	if err != nil || member.Role != "admin" {
		t.Fatalf("creator membership: %+v, %v", member, err)
	}
	releases, err := r.Queries.ListReleasesByProject(
		t.Context(),
		created.ID,
	)
	if err != nil || len(releases) != 0 {
		t.Fatal("release history copied")
	}
	return created
}

func (f projectTemplateBackendFixture) checkIndependence(
	t *testing.T,
	createdID int64,
) {
	t.Helper()
	r, template, snapshot := f.r, f.template, f.snapshot
	steps, err := r.Queries.ListStepsByProject(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if err := r.Queries.DeleteStep(t.Context(), step.ID); err != nil {
			t.Fatal(err)
		}
	}
	frozen, err := r.ReadProjectTemplate(template)
	if err != nil || !reflect.DeepEqual(frozen.Steps, snapshot.Steps) {
		t.Fatal("source edits changed template")
	}
	copiedSnapshot, err := r.CaptureProjectTemplate(t.Context(), createdID)
	if err != nil || !reflect.DeepEqual(copiedSnapshot.Steps, snapshot.Steps) {
		t.Fatal("source edits changed destination")
	}
}

func (f projectTemplateBackendFixture) checkAccessAndDeletion(t *testing.T) {
	t.Helper()
	r, actor, template, input := f.r, f.actor, f.template, f.input
	actor.Scoped = true
	if _, err := r.CreateProjectFromTemplate(
		t.Context(),
		actor,
		1,
		template.ID,
		input,
	); !errors.Is(
		err,
		ErrProjectTemplateAccess,
	) {
		t.Fatalf("scoped create allowed: %v", err)
	}
	actor.Scoped, actor.Role = false, "viewer"
	if _, err := r.SaveProjectTemplate(
		t.Context(),
		actor,
		1,
		"denied",
		"",
	); !errors.Is(
		err,
		ErrProjectTemplateAccess,
	) {
		t.Fatalf("ceiling ignored: %v", err)
	}
	if err := r.DeleteProject(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Queries.GetProjectTemplate(
		t.Context(),
		db.GetProjectTemplateParams{SourceProjectID: 1, ID: template.ID},
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatal("orphaned template retained access")
	}
}
