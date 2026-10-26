//go:build e2e

package api_test

import (
	"database/sql"
	"fmt"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectUpdateAtomicAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	lifecycle, err := f.h.repo.Queries.CreateLifecycle(t.Context(),
		db.CreateLifecycleParams{Name: "atomic update"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.SetProjectLifecycle(t.Context(),
		db.SetProjectLifecycleParams{
			ID:          f.project.ID,
			LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true},
		}); err != nil {
		t.Fatal(err)
	}
	original, err := f.h.repo.Queries.GetProject(t.Context(), f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOriginal := func() {
		t.Helper()
		actual, err := f.h.repo.Queries.GetProject(t.Context(), original.ID)
		if err != nil || actual != original {
			t.Fatalf("failed request changed project: %+v, %v", actual, err)
		}
	}
	path := fmt.Sprintf("/projects/%d", original.ID)
	if _, err := f.h.repo.DB.ExecContext(t.Context(), `
CREATE TRIGGER reject_lifecycle_update BEFORE UPDATE OF lifecycle_id ON projects
BEGIN SELECT RAISE(ABORT, 'injected lifecycle write failure'); END`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{0, lifecycle.ID} {
		f.api(t, "PUT", "/api/v1"+path, map[string]any{
			"name": "failed API edit", "description": "must roll back",
			"lifecycle_id": id,
		}, 500)
		assertOriginal()
		f.web(t, "PUT", path, url.Values{
			"name": {"failed web edit"}, "description": {"must roll back"},
			"lifecycle_id": {fmt.Sprint(id)},
		}, 500)
		assertOriginal()
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"DROP TRIGGER reject_lifecycle_update"); err != nil {
		t.Fatal(err)
	}
	f.web(t, "PUT", path, url.Values{
		"name": {"invalid lifecycle edit"}, "lifecycle_id": {"invalid"},
	}, 400)
	assertOriginal()
	duplicate := seedProject(t, f.h.repo)
	f.api(t, "PUT", "/api/v1"+path, map[string]any{
		"name": duplicate.Name, "lifecycle_id": lifecycle.ID,
	}, 409)
	assertOriginal()
	f.web(t, "PUT", path, url.Values{
		"name": {duplicate.Name}, "lifecycle_id": {fmt.Sprint(lifecycle.ID)},
	}, 422)
	assertOriginal()
	var updated struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		LifecycleID *int64 `json:"lifecycle_id"`
	}
	decodeLifecycleTest(t, f.api(t, "PUT", "/api/v1"+path, map[string]any{
		"name": "API edit", "description": "API description",
		"lifecycle_id": lifecycle.ID,
	}, 200), &updated)
	if updated.Name != "API edit" || updated.Description != "API description" ||
		updated.LifecycleID == nil || *updated.LifecycleID != lifecycle.ID {
		t.Fatalf("API response does not include complete update: %+v", updated)
	}
	f.web(t, "PUT", path, url.Values{
		"name": {"web edit"}, "description": {"web description"},
		"lifecycle_id": {""},
	}, 303)
	actual, err := f.h.repo.Queries.GetProject(t.Context(), original.ID)
	if err != nil || actual.Name != "web edit" ||
		actual.Description.String != "web description" || actual.LifecycleID.Valid {
		t.Fatalf(
			"web update did not commit complete change: %+v, %v",
			actual,
			err,
		)
	}
}
