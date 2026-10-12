//go:build e2e && packagebrowser

package api_test

import (
	"database/sql"
	"fmt"
	"testing"
)

type variableLifecycleScope struct {
	eligible, outside int64
	bound             bool
}

func (f *artifactE2E) checkVariableScopeSnapshot(
	t *testing.T, path string, scope variableLifecycleScope,
) {
	t.Helper()
	var deployments struct{ Total int }
	decodeStepLogTest(t, f.api(t, "GET", path+"/deployments", nil, 200),
		&deployments)
	if deployments.Total != 0 {
		t.Fatal("variable scenario has deployment history")
	}
	var variables struct {
		Items []struct {
			Name, Value   string
			EnvironmentID *int64 `json:"environment_id"`
		}
	}
	decodeStepLogTest(
		t,
		f.api(t, "GET", path+"/variables", nil, 200),
		&variables,
	)
	expected := map[string]string{
		"NEW": "new-scoped", "EDIT_ME": "edit-scoped",
		"DEFAULT": "override-scoped",
	}
	for name, value := range expected {
		found := false
		for _, variable := range variables.Items {
			if variable.Name == name && variable.Value == value &&
				variable.EnvironmentID != nil &&
				*variable.EnvironmentID == scope.eligible {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s scope not persisted through its browser form", name)
		}
	}
	var variable struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", path+"/variables", map[string]any{
		"name": "API_VALUE", "value": "before", "environment_id": scope.eligible,
	}, 201), &variable)
	variablePath := fmt.Sprintf("%s/variables/%d", path, variable.ID)
	f.api(t, "PUT", variablePath, map[string]any{
		"name": "API_VALUE", "value": "snapshotted", "environment_id": scope.eligible,
	}, 200)
	if scope.bound {
		f.api(t, "POST", path+"/variables", map[string]any{
			"name": "OUTSIDE", "environment_id": scope.outside,
		}, 422)
		f.api(t, "PUT", variablePath, map[string]any{
			"name": "API_VALUE", "environment_id": scope.outside,
		}, 422)
	}
	var release struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", path+"/releases",
		map[string]string{"version": "first-scoped-release"}, 201), &release)
	f.api(t, "PUT", variablePath, map[string]any{
		"name": "API_VALUE", "value": "changed", "environment_id": scope.eligible,
	}, 200)
	var snapshot struct {
		Variables []struct {
			Name, Value   string
			EnvironmentID sql.NullInt64 `json:"environment_id"`
		}
	}
	decodeStepLogTest(t, f.api(t, "GET",
		fmt.Sprintf("%s/releases/%d", path, release.ID), nil, 200), &snapshot)
	expected["API_VALUE"] = "snapshotted"
	for name, value := range expected {
		found := false
		for _, saved := range snapshot.Variables {
			if saved.Name == name && saved.Value == value &&
				saved.EnvironmentID.Valid && saved.EnvironmentID.Int64 == scope.eligible {
				found = true
			}
		}
		if !found {
			t.Fatalf("first release did not retain %s scope and value", name)
		}
	}
}
