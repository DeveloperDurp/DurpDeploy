package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"durpdeploy/internal/swagger"
)

func TestSwagger_ServesSpec(t *testing.T) {
	h := NewSwaggerHandler()

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/swagger/spec", nil)
	h.Spec(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().
		Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("expected json content-type, got %q", ct)
	}
	if rr.Body.Len() == 0 {
		t.Fatal("expected non-empty spec body")
	}
	if rr.Body.String()[0] != '{' {
		t.Fatal("expected spec to start with {")
	}
}

func TestSwagger_UIRenders(t *testing.T) {
	h := NewSwaggerHandler()

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/swagger/index.html", nil)
	h.UI(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if len(body) == 0 {
		t.Fatal("expected non-empty UI body")
	}
	if body[0] != '<' {
		t.Fatal("expected UI to start with <")
	}
}

func TestSwagger_SpecEmbedded(t *testing.T) {
	spec, err := swagger.ReadSpec()
	if err != nil {
		t.Fatalf("failed to read embedded spec: %v", err)
	}
	if len(spec) == 0 {
		t.Fatal("expected non-empty embedded spec")
	}
	if spec[0] != '{' {
		t.Fatal("expected embedded spec to start with {")
	}
}

// TestSwagger_AllPathParamsDeclared is a regression guard: every {name}
// placeholder in every path must be declared as an "in": "path" parameter on
// every operation under that path, and no path parameter may be declared for
// a name that doesn't appear in the path. The Swagger UI only surfaces
// declared parameters as injectable inputs, so a missing declaration makes
// the route un-callable from the UI.
func TestSwagger_AllPathParamsDeclared(t *testing.T) {
	spec, err := swagger.ReadSpec()
	if err != nil {
		t.Fatalf("failed to read embedded spec: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatalf("failed to parse spec: %v", err)
	}

	placeholderRE := regexp.MustCompile(`\{([^}]+)\}`)
	var missing, over []string

	for path, methods := range doc.Paths {
		expected := map[string]struct{}{}
		for _, name := range placeholderRE.FindAllStringSubmatch(path, -1) {
			expected[name[1]] = struct{}{}
		}
		for method, raw := range methods {
			if method == "parameters" {
				continue
			}
			var op struct {
				Parameters []struct {
					Name string `json:"name"`
					In   string `json:"in"`
				} `json:"parameters"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Fatalf("failed to parse %s %s: %v", method, path, err)
			}
			declared := map[string]struct{}{}
			for _, p := range op.Parameters {
				if p.In == "path" {
					declared[p.Name] = struct{}{}
				}
			}
			for name := range expected {
				if _, ok := declared[name]; !ok {
					missing = append(missing,
						method+" "+path+" missing path param {"+name+"}")
				}
			}
			for name := range declared {
				if _, ok := expected[name]; !ok {
					over = append(over,
						method+" "+path+" declares path param "+name+
							" but path placeholders are ["+
							strings.Join(sortedKeys(expected), ",")+"]")
				}
			}
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("missing path parameter declarations (%d):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(over) > 0 {
		sort.Strings(over)
		t.Errorf("over-declared path parameters (%d):\n  %s",
			len(over), strings.Join(over, "\n  "))
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
