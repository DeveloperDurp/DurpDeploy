package api_test

import (
	"encoding/json"
	"testing"

	"durpdeploy/internal/swagger"
)

func TestDeploymentFanoutAPI_SwaggerContract(t *testing.T) {
	// Given
	data, err := swagger.ReadSpec()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"definitions"`
		Paths map[string]struct {
			Get struct {
				Responses map[string]struct {
					Schema struct {
						Ref string `json:"$ref"`
					} `json:"schema"`
				} `json:"responses"`
			} `json:"get"`
		} `json:"paths"`
	}
	// When
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	// Then
	for _, name := range []string{"mode", "source", "target_mode", "agent_label_name", "agent_strategy", "aggregate_status", "children"} {
		if _, ok := spec.Definitions["DeploymentDispatch"].Properties[name]; !ok {
			t.Errorf("missing routing property %s", name)
		}
	}
	for _, name := range []string{"id", "status", "dispatch", "detail_url", "logs_url", "sse_url", "ndjson_url", "text_url"} {
		if _, ok := spec.Definitions["DeploymentChild"].Properties[name]; !ok {
			t.Errorf("missing child property %s", name)
		}
	}
	for _, path := range []string{"/deployments/{id}/logs", "/deployments/{id}/logs/{logId}", "/deployments/{id}/logs/stream", "/deployments/{id}/logs.txt"} {
		if spec.Paths[path].Get.Responses["409"].Schema.Ref != "#/definitions/FanoutParentLogConflict" {
			t.Errorf("missing parent conflict at %s", path)
		}
	}
}
