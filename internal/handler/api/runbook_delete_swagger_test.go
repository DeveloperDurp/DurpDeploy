package api_test

import (
	"encoding/json"
	"testing"

	"durpdeploy/internal/swagger"
)

func TestSwagger_RunbookDeletionContract(t *testing.T) {
	// Given: the generated public specification.
	data, err := swagger.ReadSpec()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Security []map[string][]string `json:"security"`
		Paths    map[string]map[string]struct {
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
			} `json:"parameters"`
			Responses map[string]struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	// When: a client resolves the deletion operation.
	op := spec.Paths["/projects/{id}/runbooks/{runbookId}"]["delete"]
	// Then: bearer auth, required IDs, empty success, and conflicts are documented.
	ids := map[string]bool{}
	for _, parameter := range op.Parameters {
		if parameter.In == "path" && parameter.Required {
			ids[parameter.Name] = true
		}
	}
	if !ids["id"] || !ids["runbookId"] {
		t.Fatalf("missing path IDs: %+v", op.Parameters)
	}
	for _, status := range []string{"204", "400", "401", "403", "404", "409", "413", "500"} {
		if _, ok := op.Responses[status]; !ok {
			t.Fatalf("missing response %s", status)
		}
	}
	if len(op.Responses["204"].Schema) != 0 {
		t.Fatal("204 documents a response body")
	}
	if len(spec.Security) == 0 {
		t.Fatal("missing bearer security")
	}
	if _, ok := spec.Security[0]["bearer"]; !ok {
		t.Fatalf("missing bearer auth: %+v", spec.Security)
	}
	retry := spec.Paths["/projects/{id}/runbook-executions/{executionId}/retry"]["post"]
	if _, ok := retry.Responses["404"]; !ok {
		t.Fatal("retry omits the deleted runbook response")
	}
}
