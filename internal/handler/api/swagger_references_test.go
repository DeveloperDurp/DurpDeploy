package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"durpdeploy/internal/swagger"
)

func TestSwaggerInternalReferencesResolve(t *testing.T) {
	encoded, err := swagger.ReadSpec()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	checkSwaggerReferences(t, document, document)
}

func checkSwaggerReferences(t *testing.T, document map[string]any, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		if ref, ok := value["$ref"].(string); ok &&
			strings.HasPrefix(ref, "#/") {
			if swaggerReferenceTarget(document, ref) == nil {
				t.Errorf("unresolved internal Swagger reference: %s", ref)
			}
		}
		for _, child := range value {
			checkSwaggerReferences(t, document, child)
		}
	case []any:
		for _, child := range value {
			checkSwaggerReferences(t, document, child)
		}
	}
}

func swaggerReferenceTarget(document map[string]any, ref string) any {
	var target any = document
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(
			strings.ReplaceAll(part, "~1", "/"),
			"~0",
			"~",
		)
		object, ok := target.(map[string]any)
		if !ok {
			return nil
		}
		target = object[part]
	}
	return target
}
