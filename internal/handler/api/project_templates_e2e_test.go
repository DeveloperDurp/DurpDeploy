//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

type projectTemplateE2E struct {
	staleProjectSaveFixture
	environment db.Environment
	templateID  int64
	snapshot    repository.ProjectTemplateSnapshot
	input       repository.ApplyProjectTemplate
	path        string
}

func (f projectTemplateE2E) jsonRequest(
	t *testing.T,
	method, path string,
	input any,
	status int,
) []byte {
	t.Helper()
	if input == nil {
		return f.request(t, method, path, "", f.token, "", false, status)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return f.request(
		t,
		method,
		path,
		string(encoded),
		f.token,
		"",
		false,
		status,
	)
}

func newProjectTemplateE2E(t *testing.T) projectTemplateE2E {
	t.Helper()
	f := projectTemplateE2E{
		staleProjectSaveFixture: newStaleProjectSaveFixture(t),
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.h.repo.SetSecretBox(box)
	decodeStepLogTest(
		t,
		f.request(
			t,
			"POST",
			"/api/v1/environments",
			`{"name":"template-environment"}`,
			f.adminToken,
			"",
			false,
			201,
		),
		&f.environment,
	)
	f.path = fmt.Sprintf("/api/v1/projects/%d", f.project.ID)
	for _, input := range []map[string]any{
		{"name": "COPY_DEFAULT", "value": "nonsecret-copied-default"},
		{"name": "COPY_SECRET", "value": "source-secret-must-be-absent", "secret": true, "environment_id": f.environment.ID},
		{"name": "SCOPED_DEFAULT", "value": "scoped-copied-default", "environment_id": f.environment.ID},
		{"name": "MULTILINE_DEFAULT", "value": "line1\nline2"},
		{"name": "CRLF_DEFAULT", "value": "line1\r\nline2"},
		{"name": "LEADING_DEFAULT", "value": "\nline1\nline2"},
	} {
		f.jsonRequest(t, "POST", f.path+"/variables", input, 201)
	}
	for i, interpreter := range []string{"bash", "python3", "pwsh"} {
		f.jsonRequest(t, "POST", f.path+"/steps", map[string]any{
			"name":                   "Template step " + interpreter,
			"script_body":            "echo original-template-script",
			"interpreter":            interpreter,
			"container_image":        "harbor.durp.info/dockerhub/library/bash:5.2",
			"sort_order":             i + 1,
			"timeout_seconds":        77,
			"max_retries":            2,
			"variable_names":         []string{"COPY_DEFAULT"},
			"network_mode":           "bridge",
			"approval_artifact_path": "plan.bin",
			"approval_review_path":   "review.json",
			"approval_review_format": "summary",
		}, 201)
	}
	agent, err := f.h.repo.Queries.CreateAgent(
		t.Context(),
		db.CreateAgentParams{
			ID:       "template-agent",
			Name:     "Template agent",
			Endpoint: "https://agent.example.test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.AddAgentLabel(
		t.Context(),
		db.AddAgentLabelParams{AgentID: agent.ID, Label: "template-label"},
	); err != nil {
		t.Fatal(err)
	}
	for i, mode := range []string{"host", "container"} {
		image := ""
		if mode == "container" {
			image = "harbor.durp.info/dockerhub/library/bash:5.2"
		}
		f.jsonRequest(
			t,
			"POST",
			f.path+"/steps",
			map[string]any{
				"name":                 "Agent " + mode,
				"script_body":          "echo original-template-script",
				"execution_target":     "agent",
				"agent_execution_mode": mode,
				"agent_selectors":      []string{"template-label"},
				"container_image":      image,
				"interpreter":          "bash",
				"sort_order":           i + 4,
			},
			201,
		)
	}
	f.jsonRequest(
		t,
		"POST",
		f.path+"/releases",
		map[string]string{"version": "source-frozen"},
		201,
	)
	if err := f.h.repo.Queries.UpdateProjectNotifications(
		t.Context(),
		db.UpdateProjectNotificationsParams{
			ID: f.project.ID,
			SlackWebhookUrl: sql.NullString{
				String: "source-notification-must-be-absent",
				Valid:  true,
			},
			NotifyEmails: sql.NullString{
				String: "source@example.test",
				Valid:  true,
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateRunbook(
		t.Context(),
		db.CreateRunbookParams{
			ProjectID: f.project.ID,
			Name:      "Excluded runbook",
		},
	); err != nil {
		t.Fatal(err)
	}
	f.request(
		t,
		"PUT",
		f.path+"/package-repository",
		`{"url_template":"https://packages.example.test/{version}/app.zip","auth_type":"bearer","credential":"source-package-must-be-absent"}`,
		f.adminToken,
		"",
		false,
		200,
	)
	var saved struct {
		ID       int64
		Snapshot repository.ProjectTemplateSnapshot
	}
	decodeStepLogTest(
		t,
		f.jsonRequest(
			t,
			"POST",
			f.path+"/project-templates",
			map[string]string{
				"name":        "Reusable full project",
				"description": "Template description",
			},
			201,
		),
		&saved,
	)
	f.templateID, f.snapshot = saved.ID, saved.Snapshot
	f.path += fmt.Sprintf("/project-templates/%d", saved.ID)
	f.input = repository.ApplyProjectTemplate{
		Name:        "Created from template",
		Description: &f.snapshot.Description,
		Reviewed:    true,
		EnvironmentMappings: map[string]int64{
			strconv.FormatInt(f.environment.ID, 10): f.environment.ID,
		},
		VariableValues: map[string]string{},
		PackageRepository: &repository.ProjectTemplatePackage{
			URLTemplate: f.snapshot.PackageRepository.URLTemplate,
			AuthType:    "bearer",
			Credential:  "fresh-package-value",
		},
	}
	for _, v := range f.snapshot.Variables {
		if v.Secret {
			f.input.VariableValues[v.Key] = "fresh-secret-value"
		}
	}
	return f
}

func TestProjectTemplatesAPIWebE2E(t *testing.T) {
	f := newProjectTemplateE2E(t)
	f.checkPreview(t)
	f.checkRejectedApplies(t)
	var created struct{ ID int64 }
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "POST", f.path+"/projects", f.input, 201),
		&created,
	)
	if created.ID == f.project.ID {
		t.Fatal("destination reused source identity")
	}
	createdPath := fmt.Sprintf("/api/v1/projects/%d", created.ID)
	first := f.checkCopiedConfiguration(t, created.ID, createdPath)
	f.checkExcludedConfiguration(t, createdPath)
	f.checkSnapshotIndependence(t, created.ID, createdPath, first)
	f.checkCreationAudit(t, created.ID)
	f.checkFormErrorSecrets(t)
}

func TestProjectTemplatesUnifiedListAPIWebE2E(t *testing.T) {
	f := newProjectTemplateE2E(t)
	for _, scenario := range []struct {
		path, marker string
	}{
		{"/templates", `id="templates-list"`},
		{"/templates?type=projects", "data-project-templates"},
		{"/project-templates", "data-project-templates"},
	} {
		page := f.request(
			t,
			"GET",
			scenario.path,
			"",
			"",
			f.session,
			false,
			200,
		)
		if !strings.Contains(string(page), scenario.marker) ||
			!strings.Contains(string(page), `aria-label="Template types"`) {
			t.Fatalf(
				"%s omitted the selected templates or navigation",
				scenario.path,
			)
		}
	}
	for _, boosted := range []bool{false, true} {
		f.checkUnifiedListHTMX(t, boosted)
	}
	f.request(
		t,
		"GET",
		"/templates?type=projects&offset=-1",
		"",
		"",
		f.session,
		false,
		400,
	)
	f.request(
		t,
		"GET",
		"/templates?type=projects&offset=bad",
		"",
		"",
		f.session,
		false,
		400,
	)
	page := f.request(
		t,
		"GET",
		"/project-templates?offset=50",
		"",
		"",
		f.session,
		false,
		200,
	)
	if !strings.Contains(
		string(page),
		"/templates?type=projects&amp;offset=0",
	) {
		t.Fatal("legacy pagination did not preserve the selected template type")
	}
	outsider := seedAPIUser(
		t,
		f.h.repo,
		"templates-page-outsider@example.test",
		"maintainer",
	)
	session := memberRoleSession(t, f.h, outsider.ID)
	page = f.request(
		t,
		"GET",
		"/templates?type=projects",
		"",
		"",
		session,
		false,
		200,
	)
	if !strings.Contains(string(page), "No accessible project templates.") {
		t.Fatal("unified page exposed an inaccessible project template")
	}
}

func (f projectTemplateE2E) checkUnifiedListHTMX(t *testing.T, boosted bool) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET",
		f.server.URL+"/templates?type=projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	req.Header.Set("HX-Request", "true")
	if boosted {
		req.Header.Set("HX-Boosted", "true")
	}
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	page, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 ||
		!strings.Contains(string(page), "data-project-templates") ||
		strings.Contains(string(page), "<html") != boosted {
		t.Fatalf("incorrect HTMX response, boosted=%t: %s", boosted, page)
	}
}

func (f projectTemplateE2E) checkPreview(t *testing.T) {
	t.Helper()
	preview := f.jsonRequest(t, "GET", f.path, nil, 200)
	for _, denied := range []string{"source-secret-must-be-absent", "source-package-must-be-absent", "source-notification-must-be-absent", "snapshot_json"} {
		if strings.Contains(string(preview), denied) {
			t.Fatalf("preview exposed %s", denied)
		}
	}
	if !strings.Contains(string(preview), "nonsecret-copied-default") {
		t.Fatal("non-secret defaults missing")
	}
	f.request(
		t,
		"GET",
		"/api/v1/project-templates?limit=1",
		"",
		f.token,
		"",
		false,
		200,
	)
	formPath := strings.TrimPrefix(f.path, "/api/v1")
	page := f.request(t, "GET", formPath, "", "", f.session, false, 200)
	if strings.Contains(string(page), "source-secret-must-be-absent") ||
		!strings.Contains(string(page), "data-apply-project-template") {
		t.Fatal("web preview leaked secrets or omitted form")
	}
}

func (f projectTemplateE2E) checkRejectedApplies(t *testing.T) {
	t.Helper()
	before, err := f.h.repo.Queries.CountProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		change func(*repository.ApplyProjectTemplate)
	}{
		{"review", func(p *repository.ApplyProjectTemplate) { p.Reviewed = false }},
		{"secret", func(p *repository.ApplyProjectTemplate) { p.VariableValues = nil }},
		{"mapping", func(p *repository.ApplyProjectTemplate) { p.EnvironmentMappings = nil }},
		{"environment", func(p *repository.ApplyProjectTemplate) {
			p.EnvironmentMappings = map[string]int64{strconv.FormatInt(f.environment.ID, 10): 999999}
		}},
		{"credential", func(p *repository.ApplyProjectTemplate) {
			p.PackageRepository = &repository.ProjectTemplatePackage{URLTemplate: f.input.PackageRepository.URLTemplate, AuthType: "bearer"}
		}},
		{"unknown value", func(p *repository.ApplyProjectTemplate) { p.VariableValues = map[string]string{"unknown": "value"} }},
		{"selector", func(p *repository.ApplyProjectTemplate) {
			p.AgentSelectors = map[string][]string{"4": {"missing-template-label"}}
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := f.input
			scenario.change(&input)
			f.jsonRequest(t, "POST", f.path+"/projects", input, 422)
		})
	}
	after, err := f.h.repo.Queries.CountProjects(t.Context())
	if err != nil || before != after {
		t.Fatalf(
			"failed applies created projects: %d -> %d, %v",
			before,
			after,
			err,
		)
	}
}

type projectTemplateCopiedStep struct {
	ID int64
	repository.ProjectTemplateStep
}

func (f projectTemplateE2E) checkCopiedConfiguration(
	t *testing.T,
	createdID int64,
	createdPath string,
) projectTemplateCopiedStep {
	t.Helper()
	var steps struct {
		Items []projectTemplateCopiedStep
	}
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "GET", createdPath+"/steps", nil, 200),
		&steps,
	)
	if len(steps.Items) != len(f.snapshot.Steps) {
		t.Fatal("step count changed")
	}
	for i, step := range steps.Items {
		if !reflect.DeepEqual(step.ProjectTemplateStep, f.snapshot.Steps[i]) {
			t.Fatalf(
				"step fields changed: %+v vs %+v",
				step.ProjectTemplateStep,
				f.snapshot.Steps[i],
			)
		}
	}
	values, err := f.h.repo.ListVariablesByProject(t.Context(), createdID)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.Secret != 0 && v.Value.String != "fresh-secret-value" {
			t.Fatal("fresh secret missing")
		}
	}
	return steps.Items[0]
}

func (f projectTemplateE2E) checkExcludedConfiguration(
	t *testing.T,
	createdPath string,
) {
	t.Helper()
	var releases struct{ Items []any }
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "GET", createdPath+"/releases", nil, 200),
		&releases,
	)
	if len(releases.Items) != 0 {
		t.Fatal("copied release history")
	}
	var notifications struct {
		SlackWebhookURLConfigured   bool   `json:"slack_webhook_url_configured"`
		GotifyTokenConfigured       bool   `json:"gotify_token_configured"`
		DiscordWebhookURLConfigured bool   `json:"discord_webhook_url_configured"`
		NotifyEmails                string `json:"notify_emails"`
	}
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "GET", createdPath+"/notifications", nil, 200),
		&notifications,
	)
	if notifications.NotifyEmails != "" ||
		notifications.SlackWebhookURLConfigured ||
		notifications.GotifyTokenConfigured ||
		notifications.DiscordWebhookURLConfigured {
		t.Fatal("copied notification configuration")
	}
	var runbooks struct{ Items []any }
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "GET", createdPath+"/runbooks", nil, 200),
		&runbooks,
	)
	if len(runbooks.Items) != 0 {
		t.Fatal("copied runbooks")
	}
	var members struct{ Items []any }
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "GET", createdPath+"/members", nil, 200),
		&members,
	)
	if len(members.Items) != 1 {
		t.Fatal("destination did not start with only the creator")
	}
	defaults := f.input
	defaults.Name = "Copied metadata defaults"
	defaults.Description = nil
	var copied struct{ Description string }
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "POST", f.path+"/projects", defaults, 201),
		&copied,
	)
	if copied.Description != f.snapshot.Description {
		t.Fatal("omitted description lost snapshot default")
	}
}

func (f projectTemplateE2E) checkSnapshotIndependence(
	t *testing.T,
	createdID int64,
	createdPath string,
	first projectTemplateCopiedStep,
) {
	t.Helper()
	if err := f.h.repo.RemoveProjectPackageRepository(
		t.Context(),
		createdID,
	); err != nil {
		t.Fatal(err)
	}
	f.jsonRequest(
		t,
		"POST",
		createdPath+"/releases",
		map[string]string{"version": "destination-frozen"},
		201,
	)
	first.ScriptBody = "echo changed-destination"
	f.jsonRequest(
		t,
		"PUT",
		fmt.Sprintf("%s/steps/%d", createdPath, first.ID),
		first.ProjectTemplateStep,
		200,
	)
	preview := f.jsonRequest(t, "GET", f.path, nil, 200)
	if strings.Contains(string(preview), "changed-destination") {
		t.Fatal("destination edit changed template")
	}
	var frozen struct {
		Items []struct {
			StepsJSON string `json:"steps_json"`
		}
	}
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "GET", createdPath+"/releases", nil, 200),
		&frozen,
	)
	if len(frozen.Items) != 1 ||
		strings.Contains(frozen.Items[0].StepsJSON, "changed-destination") {
		t.Fatal("destination edit changed release snapshot")
	}
	f.jsonRequest(t, "POST", f.path+"/projects", f.input, 409)
}

func (f projectTemplateE2E) checkCreationAudit(t *testing.T, createdID int64) {
	t.Helper()
	var audit struct {
		Items []struct {
			Action   string
			EntityID sql.NullInt64 `json:"entity_id"`
		}
	}
	decodeStepLogTest(
		t,
		f.request(
			t,
			"GET",
			"/api/v1/admin/audit?limit=100",
			"",
			f.adminToken,
			"",
			false,
			200,
		),
		&audit,
	)
	seenSave, seenApply := false, false
	for _, entry := range audit.Items {
		if entry.Action == "create_project_template" && entry.EntityID.Valid &&
			entry.EntityID.Int64 == f.templateID {
			seenSave = true
		}
		if entry.Action == "create_project_from_template" &&
			entry.EntityID.Valid &&
			entry.EntityID.Int64 == createdID {
			seenApply = true
		}
	}
	if !seenSave || !seenApply {
		t.Fatal("missing creation audit identities")
	}
}

func (f projectTemplateE2E) checkFormErrorSecrets(t *testing.T) {
	t.Helper()
	formPath := strings.TrimPrefix(f.path, "/api/v1")
	// Form failures return 422 and never echo submitted credentials.
	form := url.Values{
		"name":               {"web invalid"},
		"description":        {"retained draft"},
		"lifecycle_id":       {"0"},
		"reviewed":           {"true"},
		"package_url":        {f.input.PackageRepository.URLTemplate},
		"package_auth":       {"bearer"},
		"package_credential": {"submitted-package-secret"},
	}
	for _, v := range f.snapshot.Variables {
		value := "nonsecret submitted"
		if v.Secret {
			value = "submitted-secret"
		}
		form.Set("variable_"+v.Key, value)
		if v.EnvironmentID != 0 {
			form.Set(
				"environment_"+strconv.FormatInt(v.EnvironmentID, 10),
				"999999",
			)
		}
	}
	page := f.request(
		t,
		"POST",
		formPath+"/projects",
		form.Encode(),
		"",
		f.session,
		false,
		422,
	)
	if strings.Contains(string(page), "submitted-secret") ||
		strings.Contains(string(page), "submitted-package-secret") {
		t.Fatal("form error echoed a credential")
	}
}

func TestProjectTemplatesPermissionsAPIWebE2E(t *testing.T) {
	f := newProjectTemplateE2E(t)
	for _, role := range []string{"viewer", "deployer"} {
		f.changeRole(t, "project", role)
		f.jsonRequest(t, "GET", f.path, nil, 200)
		f.jsonRequest(t, "POST", f.path+"/projects", f.input, 403)
		f.jsonRequest(
			t,
			"POST",
			fmt.Sprintf("/api/v1/projects/%d/project-templates", f.project.ID),
			map[string]string{"name": "denied"},
			403,
		)
		page := f.request(
			t,
			"GET",
			strings.TrimPrefix(f.path, "/api/v1"),
			"",
			"",
			f.session,
			false,
			200,
		)
		if strings.Contains(string(page), "data-apply-project-template") {
			t.Fatal("restricted member sees apply form")
		}
	}
	f.changeRole(t, "project", "maintainer")
	row, scoped := seedAPIToken(t, f.h.repo, f.user.ID)
	if _, err := f.h.repo.ReplaceTokenScope(
		t.Context(),
		row,
		repository.TokenScope{
			Scope:      "scoped",
			Role:       "maintainer",
			ProjectIDs: []int64{f.project.ID},
		},
	); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", f.path, "", scoped, "", false, 200)
	input, err := json.Marshal(f.input)
	if err != nil {
		t.Fatal(err)
	}
	f.request(
		t,
		"POST",
		f.path+"/projects",
		string(input),
		scoped,
		"",
		false,
		403,
	)
	f.request(t, "GET", "/api/v1/project-templates", "", scoped, "", false, 403)
	outsider := seedAPIUser(
		t,
		f.h.repo,
		"template-outsider@example.test",
		"maintainer",
	)
	_, token := seedAPIToken(t, f.h.repo, outsider.ID)
	var list struct{ Total int64 }
	decodeStepLogTest(
		t,
		f.request(
			t,
			"GET",
			"/api/v1/project-templates",
			"",
			token,
			"",
			false,
			200,
		),
		&list,
	)
	if list.Total != 0 {
		t.Fatal("outsider sees template metadata")
	}
	f.request(t, "GET", f.path, "", token, "", false, 403)
	f.request(
		t,
		"POST",
		f.path+"/projects",
		string(input),
		token,
		"",
		false,
		403,
	)
	foreign := seedProject(t, f.h.repo)
	foreignPath := fmt.Sprintf(
		"/api/v1/projects/%d/project-templates/%d",
		foreign.ID,
		f.templateID,
	)
	f.request(t, "GET", foreignPath, "", f.adminToken, "", false, 404)
	lifecycle, err := f.h.repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "template-lifecycle"},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.input.LifecycleID = lifecycle.ID
	f.jsonRequest(t, "POST", f.path+"/projects", f.input, 403)
	input, err = json.Marshal(f.input)
	if err != nil {
		t.Fatal(err)
	}
	f.request(
		t,
		"POST",
		f.path+"/projects",
		string(input),
		f.adminToken,
		"",
		false,
		422,
	)
	if _, err := f.h.repo.Queries.CreateLifecycleStage(
		t.Context(),
		db.CreateLifecycleStageParams{
			LifecycleID:   lifecycle.ID,
			EnvironmentID: f.environment.ID,
			SortOrder:     1,
		},
	); err != nil {
		t.Fatal(err)
	}
	f.request(
		t,
		"POST",
		f.path+"/projects",
		string(input),
		f.adminToken,
		"",
		false,
		201,
	)
	if err := f.h.repo.Queries.RemoveProjectMember(
		t.Context(),
		db.RemoveProjectMemberParams{
			ProjectID: f.project.ID,
			UserID:    f.user.ID,
		},
	); err != nil {
		t.Fatal(err)
	}
	f.jsonRequest(t, "GET", f.path, nil, 403)
	f.jsonRequest(t, "POST", f.path+"/projects", f.input, 403)
}
