//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestLifecycleReorderAccessAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Reorder permissions"}, 201), &lifecycle)
	api := fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID)
	var stage struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", api, map[string]any{
		"environment_id": f.environment.ID, "requires_approval": true,
	}, 201), &stage)
	before, err := f.h.repo.Queries.ListLifecycleStages(
		t.Context(),
		lifecycle.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	admin := f.token
	owner, err := f.h.repo.Queries.GetSession(t.Context(), db.GetSessionParams{
		ID: f.session, ExpiresAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"maintainer", "deployer", "viewer"} {
		user := seedAPIUser(t, f.h.repo, role+"-reorder@example.test", role)
		_, f.token = seedAPIToken(t, f.h.repo, user.ID)
		f.api(t, "POST", api+"/reorder", map[string]any{
			"stage_ids": []int64{stage.ID},
		}, 403)
		// Reuse the browser session while changing its current user's role.
		if _, err := f.h.repo.DB.ExecContext(
			t.Context(),
			"UPDATE users SET role = ? WHERE id = ?",
			role,
			owner.UserID,
		); err != nil {
			t.Fatal(err)
		}
		f.web(
			t,
			"POST",
			fmt.Sprintf("/lifecycles/%d/stages/reorder", lifecycle.ID),
			url.Values{
				"stage_id":  {fmt.Sprint(stage.ID)},
				"direction": {"up"},
			},
			403,
		)
		if _, err := f.h.repo.DB.ExecContext(
			t.Context(),
			"UPDATE users SET role = 'admin' WHERE id = ?",
			owner.UserID,
		); err != nil {
			t.Fatal(err)
		}
	}
	f.token = admin
	scoped := mintProjectToken(t, f, "admin", f.project.ID)
	f.token = scoped.ID
	f.api(
		t,
		"POST",
		api+"/reorder",
		map[string]any{"stage_ids": []int64{stage.ID}},
		403,
	)
	after, err := f.h.repo.Queries.ListLifecycleStages(
		t.Context(),
		lifecycle.ID,
	)
	if err != nil || !slices.Equal(before, after) {
		t.Fatal("denied reorder changed stages", err)
	}
}

func assertLifecycleStageAccess(
	t *testing.T,
	f *artifactE2E,
	lifecycleID int64,
	setRole func(string),
) {
	t.Helper()
	stages, err := f.h.repo.Queries.ListLifecycleStages(
		t.Context(),
		lifecycleID,
	)
	if err != nil || len(stages) != 1 {
		t.Fatalf("stage fixture: %v, %v", stages, err)
	}
	stage := stages[0]
	base := fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycleID)
	web := fmt.Sprintf("/lifecycles/%d/stages", lifecycleID)
	for _, route := range []struct {
		method, suffix string
		api            any
		form           url.Values
	}{
		{"POST", "", map[string]int64{"environment_id": f.environment.ID},
			url.Values{"environment_id": {fmt.Sprint(f.environment.ID)}}},
		{"POST", "/reorder", map[string]any{"stage_ids": []int64{stage.ID}},
			url.Values{"stage_id": {fmt.Sprint(stage.ID)}, "direction": {"up"}}},
		{"PATCH", fmt.Sprintf("/%d", stage.ID),
			map[string]bool{"requires_approval": true},
			url.Values{"requires_approval": {"true"}}},
		{"POST", fmt.Sprintf("/%d/delete", stage.ID), nil, nil},
	} {
		f.api(t, route.method, base+route.suffix, route.api, 403)
		f.web(t, route.method, web+route.suffix, route.form, 403)
	}
	after, err := f.h.repo.Queries.ListLifecycleStages(t.Context(), lifecycleID)
	if err != nil || len(after) != 1 || after[0] != stage {
		t.Fatal("denied stage writes changed lifecycle", err)
	}
	// A removed stage keeps scoped values but must not reactivate without a grant.
	setRole("admin")
	f.api(t, "POST", fmt.Sprintf(
		"/api/v1/lifecycles/%d/variables", lifecycleID,
	), map[string]any{
		"name": "SCOPED_TOKEN", "value": "retained-stage-secret",
		"secret": true, "environment_id": f.environment.ID,
	}, 201)
	f.api(t, "POST", base+fmt.Sprintf("/%d/delete", stage.ID), nil, 204)
	inherited := f.base() + "/variables/inherited"
	if strings.Contains(string(f.api(t, "GET", inherited, nil, 200)),
		`"environment_id":`+fmt.Sprint(f.environment.ID)) {
		t.Fatal("removed stage still inherits scoped values")
	}
	setRole("maintainer")
	f.api(t, "POST", base,
		map[string]int64{"environment_id": f.environment.ID}, 403)
	f.web(t, "POST", web,
		url.Values{"environment_id": {fmt.Sprint(f.environment.ID)}}, 403)
	setRole("admin")
	f.web(t, "POST", web,
		url.Values{"environment_id": {fmt.Sprint(f.environment.ID)}}, 303)
	if !strings.Contains(string(f.api(t, "GET", inherited, nil, 200)),
		`"environment_id":`+fmt.Sprint(f.environment.ID)) {
		t.Fatal("admin stage restoration did not restore scoped inheritance")
	}
	setRole("maintainer")
}
