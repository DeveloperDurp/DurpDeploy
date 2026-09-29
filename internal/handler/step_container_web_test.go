package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

// Gap 16 (issue #95): the execution_target contract is only `local`
// and `agent`. Local steps now mean "run inside a server-side
// container" and require a container image; agent steps run on the
// remote agent and must not carry one. Old local steps that exist
// without an image stay visible but cannot be edited in place.

func TestStepWebCreateLocalSucceedsWithImage(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("local-with-image")

	form := url.Values{
		"name":             {"deploy"},
		"script_body":      {"Write-Output 'hi'"},
		"interpreter":      {"powershell"},
		"execution_target": {"local"},
		"container_image":  {"mcr.microsoft.com/powershell:latest"},
		"variable_names":   {"DEPLOY_ENV, API_KEY"},
		"csrf_token":       {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"status=%d, want 200 (snippet=%s)",
			response.StatusCode,
			bodySnippet(response),
		)
	}
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
	if steps[0].ExecutionTarget != "local" {
		t.Fatalf("execution_target=%q, want local", steps[0].ExecutionTarget)
	}
	if steps[0].Interpreter != "pwsh" {
		t.Fatalf("interpreter=%q, want pwsh", steps[0].Interpreter)
	}
	if steps[0].ContainerImage != "mcr.microsoft.com/powershell:latest" {
		t.Fatalf(
			"container_image=%q, want mcr.microsoft.com/powershell:latest",
			steps[0].ContainerImage,
		)
	}
	wantNames := []string{"DEPLOY_ENV", "API_KEY"}
	var got []string
	if err := json.Unmarshal([]byte(steps[0].VariableNames), &got); err != nil {
		t.Fatalf(
			"decode variable_names: %v (raw=%q)",
			err,
			steps[0].VariableNames,
		)
	}
	if fmt.Sprint(got) != fmt.Sprint(wantNames) {
		t.Fatalf(
			"variable_names=%v, want %v",
			got, wantNames,
		)
	}
}

func TestStepWebCreateLocalRejectsMissingImage(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("local-no-image")

	form := url.Values{
		"name":             {"deploy"},
		"script_body":      {"echo hi"},
		"interpreter":      {"bash"},
		"execution_target": {"local"},
		"container_image":  {""},
		"variable_names":   {""},
		"csrf_token":       {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "Container image is required") {
		t.Fatalf(
			"422 body missing required-image message; got: %s",
			body,
		)
	}
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
}

func TestStepWebCreateAgentRejectsContainerImage(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("agent-with-image")

	form := url.Values{
		"name":             {"remote"},
		"script_body":      {"hostname"},
		"interpreter":      {"bash"},
		"execution_target": {"agent"},
		"agent_label":      {""},
		"container_image":  {"alpine:3.20"},
		"variable_names":   {"DEPLOY_ENV"},
		"csrf_token":       {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "Agent steps cannot use a container image") {
		t.Fatalf(
			"422 body missing image-rejection message; got: %s",
			body,
		)
	}
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
}

func TestStepWebCreateAgentSucceedsWithoutImage(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("agent-no-image")

	form := url.Values{
		"name":             {"remote"},
		"script_body":      {"hostname"},
		"interpreter":      {"bash"},
		"execution_target": {"agent"},
		"agent_label":      {""},
		"container_image":  {""},
		"variable_names":   {"PATH"},
		"csrf_token":       {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"status=%d, want 200 (snippet=%s)",
			response.StatusCode,
			bodySnippet(response),
		)
	}
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
	if steps[0].ExecutionTarget != "agent" {
		t.Fatalf("execution_target=%q, want agent", steps[0].ExecutionTarget)
	}
	if steps[0].ContainerImage != "" {
		t.Fatalf("container_image=%q, want empty", steps[0].ContainerImage)
	}
	if steps[0].VariableNames != `["PATH"]` {
		t.Fatalf(
			"variable_names=%q, want PATH selected",
			steps[0].VariableNames,
		)
	}
}

func TestStepWebCreateRejectsUnknownTarget(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("unknown-target")

	form := url.Values{
		"name":             {"deploy"},
		"script_body":      {"echo hi"},
		"interpreter":      {"bash"},
		"execution_target": {"server"},
		"container_image":  {""},
		"variable_names":   {""},
		"csrf_token":       {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "Run step on must be Local or Agent") {
		t.Fatalf(
			"422 body missing unknown-target message; got: %s",
			body,
		)
	}
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
}

func TestStepWebCreateRejectsInvalidVariableNames(t *testing.T) {
	cases := []struct {
		name    string
		names   string
		message string
	}{
		{
			name:    "non-identifier",
			names:   "9BAD",
			message: "Variable names must be identifiers",
		},
		{
			name:    "space in name",
			names:   "BAD NAME",
			message: "Variable names must be identifiers",
		},
		{
			name:    "duplicates",
			names:   "ALPHA, ALPHA",
			message: "Variable names contain duplicates",
		},
		{
			name:    "reserved",
			names:   "HOME",
			message: "Variable name is reserved",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newProjectHarness(t)
			project := h.makeProject("local-bad-vars")

			form := url.Values{
				"name":             {"deploy"},
				"script_body":      {"echo hi"},
				"interpreter":      {"bash"},
				"execution_target": {"local"},
				"container_image":  {"alpine:3.20"},
				"variable_names":   {tc.names},
				"csrf_token":       {h.csrfToken()},
			}
			response, err := h.authedClient().PostForm(
				fmt.Sprintf(
					"%s/projects/%d/steps",
					h.server.URL, project.ID,
				),
				form,
			)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf(
					"status=%d, want 422",
					response.StatusCode,
				)
			}
			body := readBody(t, response)
			if !strings.Contains(body, tc.message) {
				t.Fatalf(
					"body missing %q; got: %s",
					tc.message, body,
				)
			}
			steps, err := h.repo.Queries.ListStepsByProject(
				t.Context(), project.ID,
			)
			if err != nil || len(steps) != 0 {
				t.Fatalf("steps=%+v error=%v", steps, err)
			}
		})
	}
}

func TestStepWebLegacyLocalUpdateRejected(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("legacy-local-update")
	step, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID:      project.ID,
			Name:           "legacy",
			ScriptBody:     "hostname",
			Interpreter:    "bash",
			ContainerImage: "",
			VariableNames:  "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"name":             {"legacy"},
		"script_body":      {"hostname"},
		"interpreter":      {"bash"},
		"execution_target": {"local"},
		"container_image":  {"alpine:3.20"},
		"variable_names":   {""},
		"csrf_token":       {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		fmt.Sprintf(
			"%s/projects/%d/steps/%d", h.server.URL, project.ID, step.ID,
		),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status=%d, want 422 (snippet=%s)",
			response.StatusCode,
			bodySnippet(response),
		)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "Legacy local step cannot be edited") {
		t.Fatalf(
			"422 body missing legacy message; got: %s",
			body,
		)
	}
	updated, err := h.repo.Queries.GetStep(t.Context(), step.ID)
	if err != nil || updated.ContainerImage != "" {
		t.Fatalf("step=%+v error=%v", updated, err)
	}
}

func TestStepWebLegacyLocalStepShowsBadge(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("legacy-host-step")
	if _, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID:      project.ID,
			Name:           "legacy",
			ScriptBody:     "hostname",
			Interpreter:    "bash",
			ContainerImage: "",
			VariableNames:  "[]",
		},
	); err != nil {
		t.Fatal(err)
	}

	response, err := h.authedClient().Get(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "data-step-legacy-badge") {
		t.Fatalf(
			"steps list missing legacy badge; got snippet: %s",
			bodySnippet(response),
		)
	}
}

func TestStepWebLegacyLocalStepEditFormShowsWarning(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("legacy-host-step-edit")
	step, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID:      project.ID,
			Name:           "legacy",
			ScriptBody:     "hostname",
			Interpreter:    "bash",
			ContainerImage: "",
			VariableNames:  "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		fmt.Sprintf(
			"%s/projects/%d/steps/%d/edit?mobile=1",
			h.server.URL, project.ID, step.ID,
		),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "data-step-legacy-warning") {
		t.Fatalf(
			"edit form missing legacy warning; got: %s",
			body,
		)
	}
}

func TestReleasePageShowsLegacyWarning(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("legacy-release")
	step, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID:      project.ID,
			Name:           "legacy",
			ScriptBody:     "hostname",
			Interpreter:    "bash",
			ContainerImage: "",
			VariableNames:  "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := []map[string]any{
		{
			"name":             step.Name,
			"script_body":      step.ScriptBody,
			"interpreter":      step.Interpreter,
			"execution_target": "local",
			"container_image":  "",
			"variable_names":   []string{},
			"sort_order":       1,
			"timeout_seconds":  0,
			"max_retries":      0,
			"agent_selectors":  []string{},
		},
	}
	encoded, _ := json.Marshal(snapshot)
	release := h.makeReleaseRaw(
		t.Context(), project.ID, "1.0.0", string(encoded),
	)

	response, err := h.authedClient().Get(
		fmt.Sprintf(
			"%s/projects/%d/releases/%d",
			h.server.URL, project.ID, release.ID,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "data-release-needs-recreation") {
		t.Fatalf(
			"release detail missing recreation warning; got: %s",
			body,
		)
	}
	if !strings.Contains(body, "data-release-step-legacy") {
		t.Fatalf(
			"release detail missing per-step legacy badge; got: %s",
			body,
		)
	}
}

func TestReleasesListShowsLegacyBadge(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("legacy-release-list")
	snapshot := []map[string]any{
		{
			"name":             "legacy",
			"script_body":      "hostname",
			"interpreter":      "bash",
			"execution_target": "local",
			"container_image":  "",
			"variable_names":   []string{},
			"sort_order":       1,
			"timeout_seconds":  0,
			"max_retries":      0,
			"agent_selectors":  []string{},
		},
	}
	encoded, _ := json.Marshal(snapshot)
	h.makeReleaseRaw(
		t.Context(), project.ID, "1.0.0", string(encoded),
	)

	response, err := h.authedClient().Get(
		fmt.Sprintf(
			"%s/projects/%d/releases", h.server.URL, project.ID,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "data-release-needs-recreation") {
		t.Fatalf(
			"releases list missing legacy badge; got: %s",
			body,
		)
	}
}

// makeReleaseRaw inserts a release row with the given snapshot JSON,
// bypassing the harness helper so the test can include the legacy
// step fields that issue #95 surfaced in the released JSON shape.
func (h *projectHarness) makeReleaseRaw(
	ctx context.Context,
	projectID int64, version, stepsJSON string,
) db.Release {
	h.t.Helper()
	release, err := h.repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: projectID,
			Version:   version,
			StepsJson: stepsJSON,
		},
	)
	if err != nil {
		h.t.Fatalf("create release: %v", err)
	}
	return release
}

// bodySnippet reads up to 400 bytes from the response body for failure
// messages; it consumes the body so callers should not use readBody
// afterwards.
func bodySnippet(response *http.Response) string {
	const max = 400
	buf := make([]byte, max)
	n, _ := response.Body.Read(buf)
	return strings.TrimSpace(string(buf[:n]))
}
