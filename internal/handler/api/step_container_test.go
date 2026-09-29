package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func createStepBody(execTarget, image string, variableNames []string) string {
	body := fmt.Sprintf(
		`{"name":"deploy","script_body":"echo hi","interpreter":"bash",`+
			`"execution_target":%q,"container_image":%q`,
		execTarget,
		image,
	)
	if len(variableNames) > 0 {
		body += `,"variable_names":[`
		for i, name := range variableNames {
			if i > 0 {
				body += ","
			}
			body += fmt.Sprintf("%q", name)
		}
		body += `]`
	}
	return body + "}"
}

func TestSteps_LocalStepRoundTrip(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))
	path := fmt.Sprintf("/api/v1/projects/%d/steps", project.ID)

	rec := h.request(
		t,
		"POST",
		path,
		token,
		createStepBody(
			"local",
			"alpine:3.20",
			[]string{"DEPLOY_ENV", "api_key"},
		),
	)
	h.assertStatus(t, rec, 201)
	assertStepContainerFields(
		t,
		rec.Body.String(),
		"alpine:3.20",
		"DEPLOY_ENV",
		"api_key",
	)

	rec = h.request(t, "GET", path+"/1", token, "")
	h.assertStatus(t, rec, 200)
	assertStepContainerFields(
		t,
		rec.Body.String(),
		"alpine:3.20",
		"DEPLOY_ENV",
		"api_key",
	)
}

func TestSteps_ServerTargetRejected(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))

	rec := h.request(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/steps", project.ID),
		token,
		createStepBody("server", "alpine:3.20", nil),
	)
	h.assertStatus(t, rec, 400)
}

func TestSteps_LocalStepRequiresContainerImage(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))

	rec := h.request(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/steps", project.ID),
		token,
		createStepBody("local", "", nil),
	)
	h.assertStatus(t, rec, 400)
}

// TestSteps_LegacyClientCreateRejected pins the contract that old
// clients posting without execution_target and without an image can
// no longer create host-executed steps.
func TestSteps_LegacyClientCreateRejected(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))

	rec := h.request(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/steps", project.ID),
		token,
		`{"name":"legacy","script_body":"echo hi","interpreter":"bash"}`,
	)
	h.assertStatus(t, rec, 400)
}

// TestSteps_ExistingImagelessLocalUpdateRejected pins that an
// image-less local step is not converted by an update that still
// omits the image; recreation is required instead.
func TestSteps_ExistingImagelessLocalUpdateRejected(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))
	path := fmt.Sprintf("/api/v1/projects/%d/steps/1", project.ID)

	if _, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID:   project.ID,
			Name:        "legacy",
			ScriptBody:  "echo hi",
			Interpreter: "bash",
		},
	); err != nil {
		t.Fatalf("seed legacy step: %v", err)
	}

	rec := h.request(
		t,
		"PUT",
		path,
		token,
		createStepBody("local", "", nil),
	)
	h.assertStatus(t, rec, 400)
}

func TestSteps_AgentStepRejectsContainerImage(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))
	h.seedAgentLabel(t, "linux")

	rec := h.request(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/steps", project.ID),
		token,
		createStepBody("agent", "alpine:3.20", nil),
	)
	h.assertStatus(t, rec, 400)
}

func TestSteps_VariableNamesValidation(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))
	path := fmt.Sprintf("/api/v1/projects/%d/steps", project.ID)

	cases := []struct {
		name string
		body string
	}{
		{
			name: "duplicate names rejected",
			body: createStepBody(
				"local",
				"alpine:3.20",
				[]string{"ALPHA", "ALPHA"},
			),
		},
		{
			name: "non identifier rejected",
			body: createStepBody(
				"local",
				"alpine:3.20",
				[]string{"9BAD"},
			),
		},
		{
			name: "space in name rejected",
			body: createStepBody(
				"local",
				"alpine:3.20",
				[]string{"BAD NAME"},
			),
		},
	}
	for _, name := range []string{
		"PATH", "HOME", "TMPDIR", "SSH_AUTH_SOCK", "CONTAINER_HOST",
		"CONTAINER_SSHKEY", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR",
		"REGISTRY_AUTH_FILE", "PODMAN_CONNECTIONS_CONF",
		"CONTAINERS_CONF", "SSH_ASKPASS",
	} {
		cases = append(cases, struct {
			name string
			body string
		}{name: name, body: createStepBody(
			"local", "alpine:3.20", []string{name},
		)})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.request(t, "POST", path, token, tc.body)
			h.assertStatus(t, rec, 400)
		})
	}
}

func TestSteps_UpdateRejectsReservedVariableName(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))
	path := fmt.Sprintf("/api/v1/projects/%d/steps", project.ID)
	rec := h.request(t, "POST", path, token,
		createStepBody("local", "alpine:3.20", nil))
	h.assertStatus(t, rec, 201)

	rec = h.request(t, "PUT", path+"/1", token,
		createStepBody("local", "alpine:3.20", []string{"HOME"}))
	h.assertStatus(t, rec, 400)
}

func TestSteps_AgentAllowsContainerReservedVariableName(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))
	rec := h.request(t, "POST",
		fmt.Sprintf("/api/v1/projects/%d/steps", project.ID), token,
		createStepBody("agent", "", []string{"PATH"}))
	h.assertStatus(t, rec, 201)
	assertStepContainerFields(t, rec.Body.String(), "", "PATH")
}

func TestTemplates_RejectReservedVariableName(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	for _, tc := range []struct {
		method, path string
	}{
		{"POST", "/api/v1/templates"},
		{"PUT", "/api/v1/templates/1"},
	} {
		if tc.method == "PUT" {
			rec := h.request(t, "POST", "/api/v1/templates", token,
				createStepBody("local", "alpine:3.20", nil))
			h.assertStatus(t, rec, 201)
		}
		rec := h.request(t, tc.method, tc.path, token,
			createStepBody("local", "alpine:3.20", []string{"CONTAINER_HOST"}))
		h.assertStatus(t, rec, 400)
	}
}

func TestSteps_UpdateStepContainerConfig(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))
	path := fmt.Sprintf("/api/v1/projects/%d/steps/1", project.ID)

	rec := h.request(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/steps", project.ID),
		token,
		createStepBody("local", "alpine:3.20", nil),
	)
	h.assertStatus(t, rec, 201)

	rec = h.request(
		t,
		"PUT",
		path,
		token,
		createStepBody("local", "debian:12", []string{"DEPLOY_ENV"}),
	)
	h.assertStatus(t, rec, 200)
	assertStepContainerFields(
		t,
		rec.Body.String(),
		"debian:12",
		"DEPLOY_ENV",
	)

	rec = h.request(
		t,
		"PUT",
		path,
		token,
		createStepBody("agent", "alpine:3.20", nil),
	)
	h.assertStatus(t, rec, 400)
}

func TestSteps_ReorderPreservesContainerConfig(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)
	project := h.seedProject(t, h.seedUser(t, "owner@example.com", "admin"))
	path := fmt.Sprintf("/api/v1/projects/%d/steps", project.ID)

	rec := h.request(
		t,
		"POST",
		path,
		token,
		createStepBody("local", "alpine:3.20", []string{"DEPLOY_ENV"}),
	)
	h.assertStatus(t, rec, 201)
	rec = h.request(
		t,
		"POST",
		path,
		token,
		createStepBody("local", "debian:12", nil),
	)
	h.assertStatus(t, rec, 201)

	rec = h.request(
		t,
		"PATCH",
		path+"/reorder",
		token,
		`{"step_ids":[2,1]}`,
	)
	h.assertStatus(t, rec, 200)
	rec = h.request(t, "GET", path+"/1", token, "")
	h.assertStatus(t, rec, 200)
	assertStepContainerFields(
		t,
		rec.Body.String(),
		"alpine:3.20",
		"DEPLOY_ENV",
	)
}

func TestTemplates_LocalTemplateRoundTrip(t *testing.T) {
	h := newHarness(t)
	token := h.adminToken(t)

	rec := h.request(
		t,
		"POST",
		"/api/v1/templates",
		token,
		`{"name":"tpl","script_body":"echo hi","interpreter":"bash",`+
			`"execution_target":"local"}`,
	)
	h.assertStatus(t, rec, 400)

	rec = h.request(
		t,
		"POST",
		"/api/v1/templates",
		token,
		`{"name":"tpl","script_body":"echo hi","interpreter":"bash",`+
			`"execution_target":"local","container_image":"alpine:3.20",`+
			`"variable_names":["DEPLOY_ENV"]}`,
	)
	h.assertStatus(t, rec, 201)
	assertStepContainerFields(
		t,
		rec.Body.String(),
		"alpine:3.20",
		"DEPLOY_ENV",
	)

	rec = h.request(t, "GET", "/api/v1/templates/1", token, "")
	h.assertStatus(t, rec, 200)
	assertStepContainerFields(
		t,
		rec.Body.String(),
		"alpine:3.20",
		"DEPLOY_ENV",
	)

	rec = h.request(t, "GET", "/api/v1/templates/1/history", token, "")
	h.assertStatus(t, rec, 200)
	var history []json.RawMessage
	if err := json.Unmarshal(
		rec.Body.Bytes(),
		&history,
	); err != nil ||
		len(history) != 1 {
		t.Fatalf("decode template history: %v (%s)", err, rec.Body.String())
	}
	assertStepContainerFields(
		t,
		string(history[0]),
		"alpine:3.20",
		"DEPLOY_ENV",
	)

	rec = h.request(
		t,
		"PUT",
		"/api/v1/templates/1",
		token,
		`{"name":"tpl","script_body":"echo hi","interpreter":"bash",`+
			`"execution_target":"local","container_image":"debian:12"}`,
	)
	h.assertStatus(t, rec, 200)
	assertStepContainerFields(t, rec.Body.String(), "debian:12")
}

func assertStepContainerFields(
	t *testing.T,
	body, wantImage string,
	wantNames ...string,
) {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if parsed["container_image"] != wantImage {
		t.Fatalf(
			"expected container_image=%q, got %v",
			wantImage,
			parsed["container_image"],
		)
	}
	var gotNames []string
	if names, ok := parsed["variable_names"].([]any); ok {
		for _, name := range names {
			s, _ := name.(string)
			gotNames = append(gotNames, s)
		}
	}
	if fmt.Sprint(gotNames) != fmt.Sprint(wantNames) {
		t.Fatalf(
			"expected variable_names=%v, got %v",
			wantNames,
			gotNames,
		)
	}
}
