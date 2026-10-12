//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
)

func assertLifecycleRetainedScopeAndDuplicateReset(
	t *testing.T, f *artifactE2E, lifecycleID int64,
) {
	t.Helper()
	stages, err := f.h.repo.Queries.ListLifecycleStages(
		t.Context(),
		lifecycleID,
	)
	if err != nil || len(stages) != 1 {
		t.Fatal("stage fixture", stages, err)
	}
	var retained struct{ ID int64 }
	base := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycleID)
	decodeLifecycleTest(t, f.api(t, "POST", base+"/variables", map[string]any{
		"name": "RETAINED", "value": "retained-secret", "secret": true,
		"environment_id": f.environment.ID,
	}, 201), &retained)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/stages/%d/delete", base, stages[0].ID),
		nil,
		204,
	)
	web := fmt.Sprintf("/lifecycles/%d/variables/%d", lifecycleID, retained.ID)
	form := url.Values{"name": {"RETAINED"}, "secret": {"on"}}
	f.web(t, "PUT", web, form, 400)
	form.Set("environment_id", "")
	f.web(t, "PUT", web, form, 400)
	stored, err := f.h.repo.GetLifecycleVariable(
		t.Context(),
		db.GetLifecycleVariableParams{
			ID:          retained.ID,
			LifecycleID: lifecycleID,
		},
	)
	if err != nil || stored.EnvironmentID.Int64 != f.environment.ID ||
		stored.Value.String != "retained-secret" {
		t.Fatal(
			"implicit scope save changed retained secret",
			stored.EnvironmentID,
			err,
		)
	}
	form.Set("environment_id", "all")
	f.web(t, "PUT", web, form, 303)
	stored, err = f.h.repo.GetLifecycleVariable(
		t.Context(),
		db.GetLifecycleVariableParams{
			ID:          retained.ID,
			LifecycleID: lifecycleID,
		},
	)
	if err != nil || stored.EnvironmentID.Valid ||
		stored.Value.String != "retained-secret" {
		t.Fatal("explicit all-stage save failed", err)
	}
	f.api(t, "POST", base+"/stages", map[string]int64{
		"environment_id": f.environment.ID,
	}, 201)
	for _, webReset := range []bool{false, true} {
		var selected struct{ ID int64 }
		for range 2 {
			decodeLifecycleTest(t, f.api(
				t,
				"POST",
				f.base()+"/variables",
				map[string]string{
					"name":  "RESET_DUP",
					"value": "local",
				},
				201,
			), &selected)
		}
		var scoped struct{ ID int64 }
		decodeLifecycleTest(
			t,
			f.api(t, "POST", f.base()+"/variables", map[string]any{
				"name":           "RESET_DUP",
				"value":          "scoped",
				"environment_id": f.environment.ID,
			}, 201),
			&scoped,
		)
		path := fmt.Sprintf(
			"%s/variables/%d?reset=inherit",
			f.base(),
			selected.ID,
		)
		if webReset {
			f.web(
				t,
				"DELETE",
				fmt.Sprintf("/projects/%d/variables/%d?reset=inherit",
					f.project.ID, selected.ID),
				nil,
				200,
			)
		} else {
			f.api(t, "DELETE", path, nil, 204)
		}
		locals, err := f.h.repo.Queries.ListVariablesByProject(
			t.Context(),
			f.project.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, local := range locals {
			if local.Name == "RESET_DUP" && !local.EnvironmentID.Valid {
				t.Fatal("reset left a duplicate override")
			}
		}
		if _, err := f.h.repo.Queries.GetVariable(
			t.Context(),
			scoped.ID,
		); err != nil {
			t.Fatal("reset deleted another scope", err)
		}
		f.api(
			t,
			"DELETE",
			fmt.Sprintf("%s/variables/%d", f.base(), scoped.ID),
			nil,
			204,
		)
	}
}
