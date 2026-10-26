//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/stepdefinition"
)

func stepFieldsForm(step stepdefinition.Definition) url.Values {
	return url.Values{
		"name": {step.Name}, "script_body": {step.ScriptBody},
		"interpreter": {
			step.Interpreter,
		}, "execution_target": {step.ExecutionTarget},
		"agent_execution_mode": {step.AgentExecutionMode},
		"agent_label":          {strings.Join(step.AgentSelectors, ",")},
		"container_image": {
			step.ContainerImage,
		}, "network_mode": {step.NetworkMode},
		"approval_artifact_path": {step.ApprovalArtifactPath},
		"approval_review_path":   {step.ApprovalReviewPath},
		"approval_review_format": {step.ApprovalReviewFormat},
		"variable_names":         {strings.Join(step.VariableNames, ",")},
		"sort_order":             {strconv.FormatInt(step.SortOrder, 10)},
		"timeout_seconds":        {strconv.FormatInt(step.TimeoutSeconds, 10)},
		"max_retries":            {strconv.FormatInt(step.MaxRetries, 10)},
	}
}

func assertStepFields(
	t *testing.T,
	raw []byte,
	want stepdefinition.Definition,
) {
	t.Helper()
	var got stepdefinition.Definition
	decodeLifecycleTest(t, raw, &got)
	if len(got.AgentSelectors) == 0 {
		got.AgentSelectors = nil
	}
	if len(got.VariableNames) == 0 {
		got.VariableNames = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("step fields\ngot  %+v\nwant %+v", got, want)
	}
}

func TestStepFieldsAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	if _, err := f.h.repo.Queries.CreateAgent(t.Context(), db.CreateAgentParams{
		ID: "fields-agent", Name: "Fields agent",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.AddAgentLabel(t.Context(), db.AddAgentLabelParams{
		AgentID: "fields-agent", Label: "canary",
	}); err != nil {
		t.Fatal(err)
	}
	webBase := fmt.Sprintf("/projects/%d", f.project.ID)
	t.Run("empty lists", func(t *testing.T) {
		step := map[string]any{
			"name": "defaults", "script_body": "true", "execution_target": "agent",
		}
		var created struct{ ID int64 }
		decodeLifecycleTest(
			t,
			f.api(t, "POST", f.base()+"/steps", step, 201),
			&created,
		)
		stored, err := f.h.repo.Queries.GetStep(t.Context(), created.ID)
		if err != nil || stored.VariableNames != "" {
			t.Fatalf(
				"API empty variable column=%q: %v",
				stored.VariableNames,
				err,
			)
		}
		var release db.Release
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				f.base()+"/releases",
				map[string]string{"version": "defaults"},
				201,
			),
			&release,
		)
		var snapshots []map[string]json.RawMessage
		decodeLifecycleTest(t, []byte(release.StepsJson), &snapshots)
		if _, exists := snapshots[0]["agent_selectors"]; exists ||
			string(snapshots[0]["variable_names"]) != "[]" {
			t.Fatalf("release optional lists=%s", release.StepsJson)
		}
		var book struct {
			Runbook db.Runbook
			Version db.RunbookVersion
		}
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				f.base()+"/runbooks",
				map[string]any{"name": "defaults", "steps": []any{step}},
				201,
			),
			&book,
		)
		assertLists := func(id int64, web bool) {
			t.Helper()
			var version struct{ Steps []map[string]json.RawMessage }
			decodeLifecycleTest(
				t,
				f.api(
					t,
					"GET",
					fmt.Sprintf(
						"%s/runbooks/%d/versions/%d",
						f.base(),
						book.Runbook.ID,
						id,
					),
					nil,
					200,
				),
				&version,
			)
			_, selectors := version.Steps[0]["agent_selectors"]
			_, names := version.Steps[0]["variable_names"]
			if selectors != web || names {
				t.Fatalf("runbook optional lists=%v", version.Steps[0])
			}
		}
		assertLists(book.Version.ID, false)
		f.web(
			t,
			"POST",
			fmt.Sprintf("%s/runbooks/%d/save", webBase, book.Runbook.ID),
			url.Values{
				"name": {
					"defaults",
				}, "step_name": {"defaults"}, "step_script": {"true"},
				"step_interpreter": {
					"bash",
				}, "step_timeout": {"0"}, "step_retries": {"0"},
				"step_target": {"agent"}, "step_selectors": {""},
			},
			303,
		)
		var detail struct{ Versions []db.RunbookVersion }
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"GET",
				fmt.Sprintf("%s/runbooks/%d", f.base(), book.Runbook.ID),
				nil,
				200,
			),
			&detail,
		)
		assertLists(detail.Versions[0].ID, true)
		f.api(
			t,
			"DELETE",
			fmt.Sprintf("%s/steps/%d", f.base(), created.ID),
			nil,
			204,
		)
	})
	for i, mode := range []string{"local", "host", "container"} {
		t.Run(mode, func(t *testing.T) {
			f.checkStepFieldsMode(t, i, mode, webBase)
		})
	}
}

func (f *artifactE2E) checkStepFieldsMode(
	t *testing.T, i int, mode, webBase string,
) {
	t.Helper()
	want := stepdefinition.Definition{
		Name: "fields-" + mode, ScriptBody: "echo original", Interpreter: "bash",
		SortOrder: int64(10*i + 1), TimeoutSeconds: 123, MaxRetries: 3,
		ExecutionTarget: "local", AgentExecutionMode: "host",
		ContainerImage: "harbor.durp.info/dockerhub/library/bash:5.2",
		NetworkMode:    "bridge", VariableNames: []string{"API_TOKEN", "DEPLOY_ENV"},
		ApprovalArtifactPath: "plan.bin", ApprovalReviewPath: "review.json",
		ApprovalReviewFormat: "summary",
	}
	if mode != "local" {
		want.ExecutionTarget, want.AgentExecutionMode = "agent", mode
		want.AgentSelectors, want.NetworkMode = []string{
			"canary",
		}, "none"
		want.ApprovalArtifactPath, want.ApprovalReviewPath, want.ApprovalReviewFormat = "", "", ""
		if mode == "host" {
			want.ContainerImage = ""
		}
	}
	var created struct{ ID int64 }
	data := f.api(t, "POST", f.base()+"/steps", want, 201)
	decodeLifecycleTest(t, data, &created)
	assertStepFields(t, data, want)
	path := fmt.Sprintf("%s/steps/%d", f.base(), created.ID)
	want.ScriptBody, want.TimeoutSeconds, want.MaxRetries = "echo edited", 86400, 10
	assertStepFields(t, f.api(t, "PUT", path, want, 200), want)
	want.ScriptBody = "echo web-edited"
	webPath := fmt.Sprintf("%s/steps/%d", webBase, created.ID)
	f.web(t, "PUT", webPath, stepFieldsForm(want), 200)
	assertStepFields(t, f.api(t, "GET", path, nil, 200), want)
	f.web(t, "POST", webPath+"/save-as-template", nil, 303)
	templates, err := f.h.repo.Queries.ListStepTemplates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var templateID int64
	for _, template := range templates {
		if template.Name == want.Name {
			templateID = template.ID
		}
	}
	if templateID == 0 {
		t.Fatal("saved template missing")
	}
	templatePath := fmt.Sprintf("/api/v1/templates/%d", templateID)
	templateWant := want
	templateWant.SortOrder, templateWant.TimeoutSeconds, templateWant.MaxRetries = 0, 0, 0
	assertStepFields(
		t,
		f.api(t, "GET", templatePath, nil, 200),
		templateWant,
	)
	// Templates deliberately reject timing fields accepted by project steps.
	f.api(t, "PUT", templatePath, templateWant, 400)
	var payload map[string]any
	encoded, err := json.Marshal(templateWant)
	if err != nil {
		t.Fatal(err)
	}
	decodeLifecycleTest(t, encoded, &payload)
	for _, key := range []string{"sort_order", "timeout_seconds", "max_retries"} {
		delete(payload, key)
	}
	payload["script_body"] = "echo template-api"
	templateWant.ScriptBody = "echo template-api"
	assertStepFields(
		t,
		f.api(t, "PUT", templatePath, payload, 200),
		templateWant,
	)
	templateWant.ScriptBody = "echo template-web"
	f.web(
		t,
		"PUT",
		fmt.Sprintf("/templates/%d", templateID),
		stepFieldsForm(templateWant),
		303,
	)
	assertStepFields(
		t,
		f.api(t, "GET", templatePath, nil, 200),
		templateWant,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("%s/steps/from-template/%d", webBase, templateID),
		nil,
		200,
	)
	var listed struct {
		Items []json.RawMessage
	}
	decodeLifecycleTest(
		t,
		f.api(t, "GET", f.base()+"/steps", nil, 200),
		&listed,
	)
	templateWant.SortOrder = want.SortOrder + 1
	assertStepFields(t, listed.Items[len(listed.Items)-1], templateWant)
	var release db.Release
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/releases",
			map[string]string{"version": mode},
			201,
		),
		&release,
	)
	var snapshots []json.RawMessage
	decodeLifecycleTest(t, []byte(release.StepsJson), &snapshots)
	assertStepFields(t, snapshots[len(snapshots)-2], want)
	assertStepFields(t, snapshots[len(snapshots)-1], templateWant)
	// Runbooks preserve execution fields but prohibit artifact approval gates.
	bookRequest := map[string]any{
		"name":  "book-" + mode,
		"steps": []any{want},
	}
	if mode == "local" {
		f.api(t, "POST", f.base()+"/runbooks", bookRequest, 422)
		want.ApprovalArtifactPath, want.ApprovalReviewPath, want.ApprovalReviewFormat = "", "", ""
		bookRequest["steps"] = []any{want}
	}
	var book struct {
		Runbook db.Runbook
		Version db.RunbookVersion
	}
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/runbooks", bookRequest, 201),
		&book,
	)
	want.SortOrder = 0
	assertVersion := func(versionID int64) {
		t.Helper()
		var version struct{ Steps []json.RawMessage }
		decodeLifecycleTest(t, f.api(t, "GET", fmt.Sprintf(
			"%s/runbooks/%d/versions/%d",
			f.base(),
			book.Runbook.ID,
			versionID,
		), nil, 200), &version)
		assertStepFields(t, version.Steps[0], want)
	}
	assertVersion(book.Version.ID)
	form := url.Values{
		"name": {book.Runbook.Name}, "step_name": {want.Name},
		"step_script": {
			"echo runbook-web",
		}, "step_interpreter": {want.Interpreter},
		"step_timeout": {"86400"}, "step_retries": {"10"},
		"step_target": {
			want.ExecutionTarget,
		}, "step_agent_mode": {want.AgentExecutionMode},
		"step_selectors": {strings.Join(want.AgentSelectors, ",")},
		"step_image": {
			want.ContainerImage,
		}, "step_network": {want.NetworkMode},
		"step_variable_names": {strings.Join(want.VariableNames, ",")},
	}
	f.web(
		t,
		"POST",
		fmt.Sprintf("%s/runbooks/%d/save", webBase, book.Runbook.ID),
		form,
		303,
	)
	var detail struct{ Versions []db.RunbookVersion }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/runbooks/%d", f.base(), book.Runbook.ID),
			nil,
			200,
		),
		&detail,
	)
	if len(detail.Versions) != 2 {
		t.Fatalf("versions=%d", len(detail.Versions))
	}
	want.ScriptBody = "echo runbook-web"
	assertVersion(detail.Versions[0].ID)
}
