//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/swagger"
)

func (f *artifactE2E) verifyArtifactSelectionErrors(t *testing.T) {
	t.Helper()
	// Given: a real repository in another project and the public API contract.
	other := seedProject(t, f.h.repo)
	body := f.api(t, "POST", fmt.Sprintf(
		"/api/v1/projects/%d/package-repositories", other.ID,
	), map[string]string{
		"name": "other", "url_template": f.upstream + "/{version}.zip",
		"auth_type": "noauth",
	}, 201)
	var repository struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &repository); err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]struct {
			Put struct {
				Responses map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"responses"`
			} `json:"put"`
		} `json:"paths"`
	}
	contract, err := swagger.ReadSpec()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contract, &spec); err != nil {
		t.Fatal(err)
	}
	responses := spec.Paths["/projects/{id}/artifact-repository"].Put.Responses
	for _, test := range []struct {
		name   string
		id     int64
		status int
		schema string
	}{
		{"negative", -1, 422, "ValidationError"},
		{"missing", 9223372036854775807, 404, "NotFoundError"},
		{"cross-project", repository.ID, 404, "NotFoundError"},
	} {
		t.Run("artifact-selection-"+test.name, func(t *testing.T) {
			// When: the API receives an invalid selection.
			body := f.api(t, "PUT", f.base()+"/artifact-repository",
				map[string]int64{"repository_id": test.id}, test.status)

			// Then: the declared error schema matches the real response.
			response := responses[fmt.Sprint(test.status)]
			if response.Schema.Ref != "#/definitions/"+test.schema {
				t.Fatalf("undeclared error schema: %+v", response)
			}
			var errorBody struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(
				body,
				&errorBody,
			); err != nil ||
				errorBody.Error == "" {
				t.Fatalf("invalid error response: %s (%v)", body, err)
			}
		})
	}
}
