package repository

import (
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectNameBackendParity(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, engine string) {
		f := newProjectTemplateBackendFixture(t, engine)
		f.checkAcceptedProjectNames(t, engine)
		f.checkRejectedProjectNames(t)
	})
}

func (f projectTemplateBackendFixture) checkAcceptedProjectNames(
	t *testing.T,
	engine string,
) {
	t.Helper()
	access := ProjectCatalogAccess{IsAdmin: 1, UserID: f.actor.UserID}
	for _, name := range []string{
		strings.Repeat("a", 255), strings.Repeat("é", 255),
		strings.Repeat("😀", 127) + "a",
	} {
		created, err := f.r.CreateProject(t.Context(),
			db.CreateProjectParams{Name: " " + name + " "}, access, nil)
		if err != nil || created.Name != name {
			t.Fatalf("valid create: %v", err)
		}
		updated, err := f.r.UpdateProject(t.Context(),
			db.UpdateProjectParams{ID: created.ID, Name: name},
			func(*Repository) error { return nil })
		if err != nil || updated.Name != name {
			t.Fatalf("valid update: %v", err)
		}
		input := f.input
		input.Name = name
		// Free the unique name before checking the template path.
		if _, err := f.r.UpdateProject(t.Context(),
			db.UpdateProjectParams{ID: created.ID, Name: "renamed-" + engine},
			func(*Repository) error { return nil }); err != nil {
			t.Fatal(err)
		}
		copied, err := f.r.CreateProjectFromTemplate(t.Context(),
			f.actor, 1, f.template.ID, input)
		if err != nil || copied.Name != name {
			t.Fatalf("valid template create: %v", err)
		}
		if err := f.r.DeleteProject(t.Context(), created.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func (f projectTemplateBackendFixture) checkRejectedProjectNames(t *testing.T) {
	t.Helper()
	access := ProjectCatalogAccess{IsAdmin: 1, UserID: f.actor.UserID}
	before, err := f.r.Queries.GetProject(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"", "  ", string([]byte{0xff}), strings.Repeat("a", 256),
		strings.Repeat("é", 256), strings.Repeat("😀", 128),
		strings.Repeat("a", 100*1024),
	} {
		if _, err := f.r.CreateProject(t.Context(),
			db.CreateProjectParams{Name: name}, access, nil); !errors.Is(err, ErrProjectName) {
			t.Fatalf("invalid create: %v", err)
		}
		if _, err := f.r.UpdateProject(t.Context(),
			db.UpdateProjectParams{ID: 1, Name: name},
			func(*Repository) error {
				t.Fatal("invalid name reached lifecycle write")
				return nil
			}); !errors.Is(err, ErrProjectName) {
			t.Fatalf("invalid update: %v", err)
		}
		input := f.input
		input.Name = name
		if _, err := f.r.CreateProjectFromTemplate(t.Context(),
			f.actor, 1, f.template.ID, input); !errors.Is(err, ErrProjectTemplateInvalid) {
			t.Fatalf("invalid template create: %v", err)
		}
	}
	after, err := f.r.Queries.GetProject(t.Context(), 1)
	if err != nil || before != after {
		t.Fatalf("invalid update changed stored project: %v", err)
	}
}
