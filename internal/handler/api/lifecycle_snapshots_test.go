package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestLifecycleVariableAPI_SnapshotInheritance(t *testing.T) {
	f := newLifecycleAPI(t)
	path := fmt.Sprintf("/api/v1/lifecycles/%d/variables", f.lifecycle.ID)
	f.request(
		t,
		"POST",
		path,
		`{"name":"REGION","value":"lifecycle-global"}`,
		201,
	)
	response := f.request(
		t,
		"POST",
		path,
		fmt.Sprintf(
			`{"name":"REGION","value":"lifecycle-env","environment_id":%d}`,
			f.environment.ID,
		),
		201,
	)
	var shared struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &shared); err != nil {
		t.Fatal(err)
	}
	projectPath := fmt.Sprintf("/api/v1/projects/%d", f.project.ID)
	f.request(
		t,
		"POST",
		projectPath+"/variables",
		`{"name":"REGION","value":"project-global"}`,
		201,
	)
	assertRelease := func(version, want string) int64 {
		t.Helper()
		created := f.request(
			t,
			"POST",
			projectPath+"/releases",
			fmt.Sprintf(`{"version":%q}`, version),
			201,
		)
		var release db.Release
		if err := json.Unmarshal(created.Body.Bytes(), &release); err != nil {
			t.Fatal(err)
		}
		assertLifecycleReleaseValue(
			t,
			f.h.repo,
			release.ID,
			f.environment.ID,
			want,
		)
		return release.ID
	}
	first := assertRelease("first", "project-global")
	assertLifecycleReleaseValue(t, f.h.repo, first, 0, "project-global")
	localResponse := f.request(
		t,
		"POST",
		projectPath+"/variables",
		fmt.Sprintf(
			`{"name":"REGION","value":"project-env","environment_id":%d}`,
			f.environment.ID,
		),
		201,
	)
	var local struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(localResponse.Body.Bytes(), &local); err != nil {
		t.Fatal(err)
	}
	assertRelease("second", "project-env")
	assertLifecycleReleaseValue(
		t,
		f.h.repo,
		first,
		f.environment.ID,
		"project-global",
	)
	f.request(
		t,
		"PUT",
		fmt.Sprintf("%s/%d", path, shared.ID),
		fmt.Sprintf(
			`{"name":"REGION","value":"edited-lifecycle-env","environment_id":%d}`,
			f.environment.ID,
		),
		200,
	)
	assertLifecycleReleaseValue(
		t,
		f.h.repo,
		first,
		f.environment.ID,
		"project-global",
	)
	f.request(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", projectPath, first),
		"",
		200,
	)
	assertLifecycleReleaseValue(
		t,
		f.h.repo,
		first,
		f.environment.ID,
		"project-env",
	)
	f.request(
		t,
		"DELETE",
		fmt.Sprintf("%s/variables/%d", projectPath, local.ID),
		"",
		204,
	)
	assertRelease("reset-to-inherited", "project-global")
	f.request(t, "DELETE", fmt.Sprintf("%s/%d", path, shared.ID), "", 204)
	assertLifecycleReleaseValue(
		t,
		f.h.repo,
		first,
		f.environment.ID,
		"project-env",
	)
	assertRelease("shared-deleted", "project-global")
	other := seedProject(t, f.h.repo)
	if err := f.h.repo.Queries.SetProjectLifecycle(t.Context(), db.SetProjectLifecycleParams{ID: other.ID, LifecycleID: sql.NullInt64{Int64: f.lifecycle.ID, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	for _, project := range []db.Project{f.project, other} {
		read := f.request(
			t,
			"GET",
			fmt.Sprintf("/api/v1/projects/%d/variables/inherited", project.ID),
			"",
			200,
		)
		if !strings.Contains(read.Body.String(), "Shared lifecycle") {
			t.Fatal("shared lifecycle source absent")
		}
	}
	unrelated := seedProject(t, f.h.repo)
	read := f.request(
		t,
		"GET",
		fmt.Sprintf("/api/v1/projects/%d/variables/inherited", unrelated.ID),
		"",
		200,
	)
	if strings.TrimSpace(read.Body.String()) != "[]" {
		t.Fatal("unrelated project inherits values")
	}
}
