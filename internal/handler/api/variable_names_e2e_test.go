//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestVariableNamesAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	base := f.base() + "/variables"
	var plain, secret struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", base, map[string]any{
		"name": "Mixed_2", "value": "keep",
	}, 201), &plain)
	decodeStepLogTest(t, f.api(t, "POST", base, map[string]any{
		"name": "Secret_2", "value": "names-secret-sentinel", "secret": true,
	}, 201), &secret)
	plainPath := fmt.Sprintf("%s/%d", base, plain.ID)
	secretPath := fmt.Sprintf("%s/%d", base, secret.ID)
	beforePlain := string(f.api(t, "GET", plainPath, nil, 200))
	beforeSecret := string(f.api(t, "GET", secretPath, nil, 200))
	for _, name := range []string{"bad name", "BAD\x00NAME", "BAD=NAME", "1BAD", "bad-name", "é", strings.Repeat("A", 256)} {
		checkVariableNameError(
			t,
			f.api(
				t,
				"POST",
				base,
				map[string]string{"name": name, "value": "rejected"},
				422,
			),
		)
		checkVariableNameError(
			t,
			f.api(
				t,
				"PUT",
				plainPath,
				map[string]string{"name": name, "value": "rejected"},
				422,
			),
		)
		checkVariableNameError(
			t,
			f.api(
				t,
				"PUT",
				secretPath,
				map[string]any{"name": name, "secret": true, "value": ""},
				422,
			),
		)
		page := f.web(
			t,
			"POST",
			strings.TrimPrefix(base, "/api/v1"),
			url.Values{"name": {name}, "value": {"rejected"}},
			422,
		)
		checkVariableNameError(t, []byte(page))
	}
	if string(f.api(t, "GET", plainPath, nil, 200)) != beforePlain ||
		string(f.api(t, "GET", secretPath, nil, 200)) != beforeSecret {
		t.Fatal("invalid update changed the variable or its secret metadata")
	}
	var stored struct{ Items []struct{ ID int64 } }
	decodeStepLogTest(t, f.api(t, "GET", base, nil, 200), &stored)
	if len(stored.Items) != 2 {
		t.Fatal("invalid creates persisted variables")
	}
	for _, name := range []string{"_", "lower_2", strings.Repeat("A", 255), "PATH"} {
		f.api(
			t,
			"POST",
			base,
			map[string]string{"name": name, "value": ""},
			201,
		)
	}
	f.api(
		t,
		"POST",
		base,
		map[string]any{
			"name":           "Mixed_2",
			"value":          "scoped",
			"environment_id": f.environment.ID,
		},
		201,
	)
	f.api(
		t,
		"PUT",
		secretPath,
		map[string]any{"name": "Renamed_2", "secret": true, "value": ""},
		200,
	)
	for _, name := range []string{"ARTIFACT_PATH", "DURPDEPLOY_STAGE_DIR", "DURPDEPLOY_APPROVED_DIR", "DURPDEPLOY_ENVIRONMENT_NAME", "DURPDEPLOY_ENVIRONMENT_ID"} {
		f.api(t, "POST", base, map[string]string{"name": name}, 422)
		f.api(t, "PUT", plainPath, map[string]string{"name": name}, 422)
		f.api(
			t,
			"PUT",
			secretPath,
			map[string]any{"name": name, "secret": true, "value": ""},
			422,
		)
		f.web(
			t,
			"POST",
			strings.TrimPrefix(base, "/api/v1"),
			url.Values{"name": {name}},
			422,
		)
	}
	verificationRelease(t, f, "valid-names")
}

func checkVariableNameError(t *testing.T, body []byte) {
	t.Helper()
	if !strings.Contains(string(body), "1-255 ASCII") ||
		!strings.Contains(
			string(body),
			"starting with a letter or underscore",
		) {
		t.Fatalf("unsupported name has no format explanation: %s", body)
	}
}
