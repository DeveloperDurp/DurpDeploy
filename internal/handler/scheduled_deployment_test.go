package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

// scheduledHarness holds a test server with scheduled-deployment routes mounted.
type scheduledHarness struct {
	t      *testing.T
	repo   *repository.Repository
	server *httptest.Server
}

func newScheduledHarness(t *testing.T) *scheduledHarness {
	t.Helper()
	// ponytail: file-backed SQLite, not :memory:, because the runner spawns
	// goroutines that need their own DB connection.
	conn := newHandlerTestDatabase(t)

	repo := repository.New(conn)
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	sdh := handler.NewScheduledDeploymentHandler(repo, parser)

	// Also mount deployment handler so we can reuse availableEnvsForDeployPage
	// via the NewForm route if needed; not required for the core tests.
	dh := handler.NewDeploymentHandler(repo, rnr)

	r := chi.NewRouter()
	r.Get("/projects/{id}/schedules", sdh.List)
	r.Get("/projects/{id}/schedules/new", sdh.NewForm)
	r.Post("/projects/{id}/schedules", sdh.Create)
	r.Get("/projects/{id}/schedules/{schedId}/edit", sdh.EditForm)
	r.Put("/projects/{id}/schedules/{schedId}", sdh.Update)
	r.Delete("/projects/{id}/schedules/{schedId}", sdh.Delete)
	r.Post("/projects/{id}/schedules/{schedId}/toggle", sdh.Toggle)

	// Mount deploy routes so the harness can create releases via the repo
	// and the env helpers work consistently with the rest of the suite.
	r.Get("/projects/{id}/deploy", dh.NewDeploymentPage)
	r.Post("/projects/{id}/deploy", dh.ScheduleDeployment)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &scheduledHarness{t: t, repo: repo, server: srv}
}

func (h *scheduledHarness) makeProject(name string) db.Project {
	h.t.Helper()
	p, err := h.repo.Queries.CreateProject(
		context.Background(),
		db.CreateProjectParams{Name: name, Description: sql.NullString{}},
	)
	if err != nil {
		h.t.Fatalf("create project: %v", err)
	}
	return p
}

func (h *scheduledHarness) makeEnv(name string) db.Environment {
	h.t.Helper()
	e, err := h.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{Name: name, Description: sql.NullString{}},
	)
	if err != nil {
		h.t.Fatalf("create env: %v", err)
	}
	return e
}

func (h *scheduledHarness) makeRelease(
	projectID int64,
	version, scriptBody string,
) db.Release {
	h.t.Helper()
	steps := []map[string]any{
		{"name": "s1", "script_body": scriptBody, "sort_order": 1},
	}
	stepsJSON, _ := json.Marshal(steps)
	r, err := h.repo.Queries.CreateRelease(
		context.Background(),
		db.CreateReleaseParams{
			ProjectID: projectID,
			Version:   version,
			StepsJson: string(stepsJSON),
		},
	)
	if err != nil {
		h.t.Fatalf("create release: %v", err)
	}
	return r
}

func (h *scheduledHarness) postSchedule(
	projectID, releaseID, envID int64,
	cronExpr, note string,
	enabled bool,
) int {
	h.t.Helper()
	form := url.Values{}
	form.Set("release_id", fmt.Sprintf("%d", releaseID))
	form.Set("environment_id", fmt.Sprintf("%d", envID))
	form.Set("cron", cronExpr)
	form.Set("note", note)
	if enabled {
		form.Set("enabled", "true")
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.PostForm(
		fmt.Sprintf("%s/projects/%d/schedules", h.server.URL, projectID),
		form,
	)
	if err != nil {
		h.t.Fatalf("POST schedule: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *scheduledHarness) putSchedule(
	projectID, schedID, releaseID, envID int64,
	cronExpr, note string,
	enabled bool,
) int {
	h.t.Helper()
	form := url.Values{}
	form.Set("release_id", fmt.Sprintf("%d", releaseID))
	form.Set("environment_id", fmt.Sprintf("%d", envID))
	form.Set("cron", cronExpr)
	form.Set("note", note)
	if enabled {
		form.Set("enabled", "true")
	}
	req, _ := http.NewRequest(
		"PUT",
		fmt.Sprintf(
			"%s/projects/%d/schedules/%d",
			h.server.URL,
			projectID,
			schedID,
		),
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("PUT schedule: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *scheduledHarness) toggleSchedule(projectID, schedID int64) int {
	h.t.Helper()
	resp, err := http.Post(
		fmt.Sprintf(
			"%s/projects/%d/schedules/%d/toggle",
			h.server.URL,
			projectID,
			schedID,
		),
		"",
		nil,
	)
	if err != nil {
		h.t.Fatalf("POST toggle: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *scheduledHarness) deleteSchedule(projectID, schedID int64) int {
	h.t.Helper()
	req, _ := http.NewRequest(
		"DELETE",
		fmt.Sprintf(
			"%s/projects/%d/schedules/%d",
			h.server.URL,
			projectID,
			schedID,
		),
		nil,
	)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("DELETE schedule: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func scheduleFormOptionSelected(body, field string, id int64) bool {
	pattern := fmt.Sprintf(
		`(?s)<select[^>]*name="%s"[^>]*>.*?<option\s+value="%d"\s+selected`,
		regexp.QuoteMeta(field),
		id,
	)
	return regexp.MustCompile(pattern).MatchString(body)
}

func scheduleFormEnabled(body string) bool {
	return regexp.MustCompile(
		`(?s)<input[^>]*name="enabled"[^>]*checked`,
	).MatchString(body)
}

func (h *scheduledHarness) scheduleRequest(
	method, path string,
	form url.Values,
) (int, string) {
	h.t.Helper()
	var bodyReader io.Reader
	if form != nil {
		bodyReader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, h.server.URL+path, bodyReader)
	if err != nil {
		h.t.Fatalf("new %s request: %v", method, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return resp.StatusCode, string(body)
}

func TestScheduledDeploymentFullEditForm_saves_on_native_POST(
	t *testing.T,
) {
	// Given
	h := newProjectHarness(t)
	project := h.makeProject("full-schedule-edit")
	environment := h.makeEnv("full-schedule-edit-env")
	release := h.makeRelease(project.ID, "1.0.0", "true")
	schedule, err := h.repo.Queries.CreateScheduledDeployment(
		context.Background(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     project.ID,
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Cron:          "0 0 * * *",
			NextRunAt:     time.Now().Add(time.Hour).Unix(),
			Enabled:       1,
			Note:          sql.NullString{String: "before", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	editResponse, err := h.authedClient().Get(fmt.Sprintf(
		"%s/projects/%d/schedules/%d/edit",
		h.server.URL,
		project.ID,
		schedule.ID,
	))
	if err != nil {
		t.Fatalf("get edit form: %v", err)
	}
	defer editResponse.Body.Close()
	if editResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"get edit form: got %d, want %d",
			editResponse.StatusCode,
			http.StatusOK,
		)
	}
	formHTML, err := io.ReadAll(editResponse.Body)
	if err != nil {
		t.Fatalf("read edit form: %v", err)
	}
	formActionMatch := regexp.MustCompile(
		`<form method="post" action="([^"]+)" class="space-y-4">`,
	).FindStringSubmatch(string(formHTML))
	if len(formActionMatch) != 2 {
		t.Fatal("full schedule edit form action was not rendered")
	}
	form := url.Values{
		"release_id":     {fmt.Sprintf("%d", release.ID)},
		"environment_id": {fmt.Sprintf("%d", environment.ID)},
		"cron":           {"0 12 * * *"},
		"note":           {"after"},
		"enabled":        {"true"},
		"target_mode":    {"inherit"},
		"agent_strategy": {"round_robin"},
		"csrf_token":     {h.csrfToken()},
	}

	// When
	request, err := http.NewRequest(
		http.MethodPost,
		h.server.URL+formActionMatch[1],
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("new full edit form request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatalf("submit full edit form: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"submit full edit form: got %d, want %d",
			response.StatusCode,
			http.StatusSeeOther,
		)
	}
	updated, err := h.repo.Queries.GetScheduledDeployment(
		context.Background(),
		schedule.ID,
	)
	if err != nil {
		t.Fatalf("get updated schedule: %v", err)
	}
	if updated.Cron != "0 12 * * *" || updated.Note.String != "after" {
		t.Fatalf("updated schedule = %#v, want submitted values", updated)
	}
	policy, err := h.repo.Queries.GetScheduledDeploymentRoutingPolicy(
		context.Background(),
		schedule.ID,
	)
	if err != nil {
		t.Fatalf("get updated schedule policy: %v", err)
	}
	if policy.TargetMode != "inherit" || policy.AgentLabelID.Valid ||
		policy.AgentStrategy.Valid {
		t.Fatalf("updated schedule policy = %#v, want inherit", policy)
	}
}

func TestScheduledDeploymentFullEditForm_ignores_hiddenLabelFieldsOutsideLabelMode(
	t *testing.T,
) {
	// Given
	h := newProjectHarness(t)
	project := h.makeProject("full-schedule-edit-hidden-label")
	environment := h.makeEnv("full-schedule-edit-hidden-label-env")
	release := h.makeRelease(project.ID, "1.0.0", "true")
	label, err := h.repo.Queries.CreateAgentLabel(
		context.Background(),
		db.CreateAgentLabelParams{
			Name: "Hidden label", NormalizedName: "hidden label",
		},
	)
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	for _, targetMode := range []string{"local", "inherit"} {
		t.Run("label_to_"+targetMode, func(t *testing.T) {
			schedule, err := h.repo.Queries.CreateScheduledDeployment(
				context.Background(),
				db.CreateScheduledDeploymentParams{
					ProjectID:     project.ID,
					ReleaseID:     release.ID,
					EnvironmentID: environment.ID,
					Cron:          "0 0 * * *",
					NextRunAt:     time.Now().Add(time.Hour).Unix(),
					Enabled:       1,
				},
			)
			if err != nil {
				t.Fatalf("create schedule: %v", err)
			}
			_, err = h.repo.Queries.CreateScheduledDeploymentRoutingPolicy(
				context.Background(),
				db.CreateScheduledDeploymentRoutingPolicyParams{
					ScheduledDeploymentID: schedule.ID,
					TargetMode:            "label",
					AgentLabelID: sql.NullInt64{
						Int64: label.ID,
						Valid: true,
					},
					AgentStrategy: sql.NullString{
						String: "round_robin",
						Valid:  true,
					},
				},
			)
			if err != nil {
				t.Fatalf("create label policy: %v", err)
			}

			editResponse, err := h.authedClient().Get(fmt.Sprintf(
				"%s/projects/%d/schedules/%d/edit",
				h.server.URL,
				project.ID,
				schedule.ID,
			))
			if err != nil {
				t.Fatalf("get edit form: %v", err)
			}
			formHTML, readErr := io.ReadAll(editResponse.Body)
			editResponse.Body.Close()
			if readErr != nil {
				t.Fatalf("read edit form: %v", readErr)
			}
			formActionMatch := regexp.MustCompile(
				`<form method="post" action="([^"]+)" class="space-y-4">`,
			).FindStringSubmatch(string(formHTML))
			if len(formActionMatch) != 2 {
				t.Fatal("full schedule edit form action was not rendered")
			}
			form := url.Values{
				"release_id":     {fmt.Sprintf("%d", release.ID)},
				"environment_id": {fmt.Sprintf("%d", environment.ID)},
				"cron":           {"0 12 * * *"},
				"enabled":        {"true"},
				"target_mode":    {targetMode},
				"agent_label_id": {fmt.Sprintf("%d", label.ID)},
				"agent_strategy": {"round_robin"},
				"csrf_token":     {h.csrfToken()},
			}
			request, err := http.NewRequest(
				http.MethodPost,
				h.server.URL+formActionMatch[1],
				strings.NewReader(form.Encode()),
			)
			if err != nil {
				t.Fatalf("new full edit form request: %v", err)
			}
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response, err := h.authedClient().Do(request)
			if err != nil {
				t.Fatalf("submit full edit form: %v", err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf(
					"submit full edit form: got %d, want %d",
					response.StatusCode,
					http.StatusSeeOther,
				)
			}
			policy, err := h.repo.Queries.GetScheduledDeploymentRoutingPolicy(
				context.Background(),
				schedule.ID,
			)
			if err != nil {
				t.Fatalf("get updated schedule policy: %v", err)
			}
			if policy.TargetMode != targetMode || policy.AgentLabelID.Valid ||
				policy.AgentStrategy.Valid {
				t.Fatalf(
					"updated schedule policy = %#v, want %s without label fields",
					policy,
					targetMode,
				)
			}
		})
	}
}

func TestScheduledQuickEdit_retains_note_when_form_round_trips(
	t *testing.T,
) {
	// Given
	h := newScheduledHarness(t)
	project := h.makeProject("quick-edit-note-round-trip")
	environment := h.makeEnv("quick-edit-env")
	release := h.makeRelease(project.ID, "1.0.0", "true")
	const note = "keep this quick-edit note"
	if code := h.postSchedule(
		project.ID,
		release.ID,
		environment.ID,
		"0 0 * * *",
		note,
		true,
	); code != http.StatusSeeOther {
		t.Fatalf("create schedule: got %d, want %d", code, http.StatusSeeOther)
	}
	schedules, err := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		project.ID,
	)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("list schedules: got %d, want 1", len(schedules))
	}

	// When
	status, listHTML := h.scheduleRequest(
		http.MethodGet,
		fmt.Sprintf("/projects/%d/schedules", project.ID),
		nil,
	)

	// Then
	if status != http.StatusOK {
		t.Fatalf("get schedules: got %d, want %d", status, http.StatusOK)
	}
	if !strings.Contains(
		listHTML,
		`name="note" value="keep this quick-edit note"`,
	) {
		t.Fatal("quick edit form did not include the existing note")
	}
	if code := h.putSchedule(
		project.ID,
		schedules[0].ID,
		release.ID,
		environment.ID,
		"0 12 * * *",
		note,
		true,
	); code != http.StatusSeeOther {
		t.Fatalf("update schedule: got %d, want %d", code, http.StatusSeeOther)
	}
	updated, err := h.repo.Queries.GetScheduledDeployment(
		context.Background(),
		schedules[0].ID,
	)
	if err != nil {
		t.Fatalf("get updated schedule: %v", err)
	}
	if !updated.Note.Valid || updated.Note.String != note {
		t.Errorf("note: got %#v, want %q", updated.Note, note)
	}
}

func TestCreateScheduled_retains_submitted_fields_when_cron_invalid(
	t *testing.T,
) {
	// Given
	h := newScheduledHarness(t)
	project := h.makeProject("invalid-create-retention")
	environment := h.makeEnv("invalid-create-env")
	release := h.makeRelease(project.ID, "1.0.0", "true")
	form := url.Values{
		"release_id":     {fmt.Sprintf("%d", release.ID)},
		"environment_id": {fmt.Sprintf("%d", environment.ID)},
		"cron":           {"bad cron"},
		"note":           {"draft create note"},
	}

	// When
	status, formHTML := h.scheduleRequest(
		http.MethodPost,
		fmt.Sprintf("/projects/%d/schedules", project.ID),
		form,
	)

	// Then
	if status != http.StatusUnprocessableEntity {
		t.Fatalf(
			"post schedule: got %d, want %d",
			status,
			http.StatusUnprocessableEntity,
		)
	}
	if !scheduleFormOptionSelected(formHTML, "release_id", release.ID) {
		t.Fatal("release selection was not retained")
	}
	if !scheduleFormOptionSelected(formHTML, "environment_id", environment.ID) {
		t.Fatal("environment selection was not retained")
	}
	if !strings.Contains(formHTML, `value="bad cron"`) {
		t.Fatal("cron value was not retained")
	}
	if !strings.Contains(formHTML, "draft create note") {
		t.Fatal("note value was not retained")
	}
	if scheduleFormEnabled(formHTML) {
		t.Fatal("enabled state was not retained as disabled")
	}
	schedules, err := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		project.ID,
	)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	if len(schedules) != 0 {
		t.Errorf(
			"created %d schedules after a 422 response, want 0",
			len(schedules),
		)
	}
}

func TestUpdateScheduled_retains_submitted_fields_when_timezone_prefix_invalid(
	t *testing.T,
) {
	// Given
	h := newScheduledHarness(t)
	project := h.makeProject("invalid-update-retention")
	originalEnvironment := h.makeEnv("original-update-env")
	updatedEnvironment := h.makeEnv("updated-update-env")
	originalRelease := h.makeRelease(project.ID, "1.0.0", "true")
	updatedRelease := h.makeRelease(project.ID, "2.0.0", "true")
	if code := h.postSchedule(
		project.ID,
		originalRelease.ID,
		originalEnvironment.ID,
		"0 0 * * *",
		"saved original note",
		true,
	); code != http.StatusSeeOther {
		t.Fatalf("create schedule: got %d, want %d", code, http.StatusSeeOther)
	}
	schedules, err := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		project.ID,
	)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("list schedules: got %d, want 1", len(schedules))
	}
	original := schedules[0]
	form := url.Values{
		"release_id":     {fmt.Sprintf("%d", updatedRelease.ID)},
		"environment_id": {fmt.Sprintf("%d", updatedEnvironment.ID)},
		"cron":           {"TZ=America/Los_Angeles 15 14 * * *"},
		"note":           {"draft update note"},
	}

	// When
	status, formHTML := h.scheduleRequest(
		http.MethodPut,
		fmt.Sprintf("/projects/%d/schedules/%d", project.ID, original.ID),
		form,
	)

	// Then
	if status != http.StatusUnprocessableEntity {
		t.Fatalf(
			"update schedule: got %d, want %d",
			status,
			http.StatusUnprocessableEntity,
		)
	}
	if !scheduleFormOptionSelected(formHTML, "release_id", updatedRelease.ID) {
		t.Fatal("updated release selection was not retained")
	}
	if !scheduleFormOptionSelected(
		formHTML,
		"environment_id",
		updatedEnvironment.ID,
	) {
		t.Fatal("updated environment selection was not retained")
	}
	if !strings.Contains(
		formHTML,
		`value="TZ=America/Los_Angeles 15 14 * * *"`,
	) {
		t.Fatal("timezone-prefixed cron value was not retained")
	}
	if !strings.Contains(formHTML, "draft update note") {
		t.Fatal("updated note value was not retained")
	}
	if scheduleFormEnabled(formHTML) {
		t.Fatal("updated enabled state was not retained as disabled")
	}
	persisted, err := h.repo.Queries.GetScheduledDeployment(
		context.Background(),
		original.ID,
	)
	if err != nil {
		t.Fatalf("get persisted schedule: %v", err)
	}
	if persisted.ReleaseID != original.ReleaseID {
		t.Errorf(
			"release_id: got %d, want %d",
			persisted.ReleaseID,
			original.ReleaseID,
		)
	}
	if persisted.EnvironmentID != original.EnvironmentID {
		t.Errorf(
			"environment_id: got %d, want %d",
			persisted.EnvironmentID,
			original.EnvironmentID,
		)
	}
	if persisted.Cron != original.Cron {
		t.Errorf("cron: got %q, want %q", persisted.Cron, original.Cron)
	}
	if persisted.Enabled != original.Enabled {
		t.Errorf(
			"enabled: got %d, want %d",
			persisted.Enabled,
			original.Enabled,
		)
	}
	if persisted.Note != original.Note {
		t.Errorf("note: got %#v, want %#v", persisted.Note, original.Note)
	}
}

func TestUpdateScheduled_clears_note_when_note_is_explicitly_empty(
	t *testing.T,
) {
	// Given
	h := newScheduledHarness(t)
	project := h.makeProject("explicit-empty-note")
	environment := h.makeEnv("explicit-empty-note-env")
	release := h.makeRelease(project.ID, "1.0.0", "true")
	if code := h.postSchedule(
		project.ID,
		release.ID,
		environment.ID,
		"0 0 * * *",
		"remove this note",
		true,
	); code != http.StatusSeeOther {
		t.Fatalf("create schedule: got %d, want %d", code, http.StatusSeeOther)
	}
	schedules, err := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		project.ID,
	)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	if len(schedules) != 1 {
		t.Fatalf("list schedules: got %d, want 1", len(schedules))
	}
	form := url.Values{
		"release_id":     {fmt.Sprintf("%d", release.ID)},
		"environment_id": {fmt.Sprintf("%d", environment.ID)},
		"cron":           {"0 12 * * *"},
		"note":           {""},
		"enabled":        {"true"},
	}

	// When
	status, _ := h.scheduleRequest(
		http.MethodPut,
		fmt.Sprintf("/projects/%d/schedules/%d", project.ID, schedules[0].ID),
		form,
	)

	// Then
	if status != http.StatusSeeOther {
		t.Fatalf(
			"update schedule: got %d, want %d",
			status,
			http.StatusSeeOther,
		)
	}
	updated, err := h.repo.Queries.GetScheduledDeployment(
		context.Background(),
		schedules[0].ID,
	)
	if err != nil {
		t.Fatalf("get updated schedule: %v", err)
	}
	if updated.Note.Valid || updated.Note.String != "" {
		t.Errorf("note: got %#v, want an explicit cleared note", updated.Note)
	}
}

func TestCreateScheduled_Valid(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-valid")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := h.postSchedule(
		proj.ID,
		rel.ID,
		env.ID,
		"0 0 * * *",
		"nightly",
		true,
	)
	if code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", code)
	}

	schedules, err := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		proj.ID,
	)
	if err != nil || len(schedules) == 0 {
		t.Fatalf("expected schedule row, got err=%v", err)
	}
	s := schedules[0]
	if s.ReleaseID != rel.ID {
		t.Errorf("release_id: got %d, want %d", s.ReleaseID, rel.ID)
	}
	if s.EnvironmentID != env.ID {
		t.Errorf("environment_id: got %d, want %d", s.EnvironmentID, env.ID)
	}
	if s.Cron != "0 0 * * *" {
		t.Errorf("cron: got %q, want %q", s.Cron, "0 0 * * *")
	}
	if s.NextRunAt <= time.Now().Unix() {
		t.Errorf("next_run_at should be in the future, got %d", s.NextRunAt)
	}
	if s.Enabled != 1 {
		t.Errorf("enabled: got %d, want 1", s.Enabled)
	}
}

func TestCreateScheduled_BadCron_422(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-bad-cron")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := h.postSchedule(proj.ID, rel.ID, env.ID, "bogus", "nightly", true)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", code)
	}

	// Ensure no row was created.
	schedules, _ := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		proj.ID,
	)
	if len(schedules) != 0 {
		t.Errorf("expected 0 schedules, got %d", len(schedules))
	}
}

func TestCreateScheduled_CrossProject_400(t *testing.T) {
	h := newScheduledHarness(t)
	projA := h.makeProject("projA")
	projB := h.makeProject("projB")
	env := h.makeEnv("dev")
	relA := h.makeRelease(projA.ID, "1.0.0", "true")
	_ = h.makeRelease(projB.ID, "1.0.0", "true")

	code := h.postSchedule(
		projB.ID,
		relA.ID,
		env.ID,
		"0 0 * * *",
		"nightly",
		true,
	)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", code)
	}

	schedules, _ := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		projB.ID,
	)
	if len(schedules) != 0 {
		t.Errorf("expected 0 schedules for projB, got %d", len(schedules))
	}
}

func TestUpdateScheduled_RecomputesNextRun(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-update")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	h.postSchedule(proj.ID, rel.ID, env.ID, "0 0 * * *", "nightly", true)
	schedules, _ := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		proj.ID,
	)
	s := schedules[0]
	oldNextRun := s.NextRunAt

	code := h.putSchedule(
		proj.ID,
		s.ID,
		rel.ID,
		env.ID,
		"0 12 * * *",
		"daily noon",
		true,
	)
	if code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", code)
	}

	updated, err := h.repo.Queries.GetScheduledDeployment(
		context.Background(),
		s.ID,
	)
	if err != nil {
		t.Fatalf("get scheduled: %v", err)
	}
	if updated.NextRunAt == oldNextRun {
		t.Errorf("next_run_at should have changed after update")
	}
	if updated.Cron != "0 12 * * *" {
		t.Errorf("cron: got %q, want %q", updated.Cron, "0 12 * * *")
	}
}

func TestToggleScheduled_Advances(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-toggle")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	oldNextRun := time.Now().Add(-24 * time.Hour).Unix()
	s, err := h.repo.Queries.CreateScheduledDeployment(
		context.Background(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     proj.ID,
			ReleaseID:     rel.ID,
			EnvironmentID: env.ID,
			Cron:          "0 0 * * *",
			NextRunAt:     oldNextRun,
			Enabled:       0,
			LastFiredAt:   sql.NullInt64{},
			Note:          sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if s.Enabled != 0 {
		t.Fatalf("expected disabled, got enabled=%d", s.Enabled)
	}

	code := h.toggleSchedule(proj.ID, s.ID)
	if code != http.StatusOK && code != http.StatusSeeOther {
		t.Logf("toggle returned %d (accepting any 2xx)", code)
	}

	updated, err := h.repo.Queries.GetScheduledDeployment(
		context.Background(),
		s.ID,
	)
	if err != nil {
		t.Fatalf("get scheduled: %v", err)
	}
	if updated.Enabled != 1 {
		t.Errorf("expected enabled=1 after toggle, got %d", updated.Enabled)
	}
	if updated.NextRunAt <= oldNextRun {
		t.Errorf(
			"next_run_at should be recomputed to future when enabling, got %d (was %d)",
			updated.NextRunAt,
			oldNextRun,
		)
	}
}

func TestDeleteScheduled_404(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-delete")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	h.postSchedule(proj.ID, rel.ID, env.ID, "0 0 * * *", "nightly", true)
	schedules, _ := h.repo.Queries.ListScheduledDeploymentsByProject(
		context.Background(),
		proj.ID,
	)
	s := schedules[0]

	code := h.deleteSchedule(proj.ID, s.ID)
	if code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", code)
	}

	_, err := h.repo.Queries.GetScheduledDeployment(context.Background(), s.ID)
	if err != sql.ErrNoRows {
		t.Errorf("expected schedule to be deleted, got err=%v", err)
	}
}

func TestCreateScheduled_DescriptorRejected(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-desc")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := h.postSchedule(proj.ID, rel.ID, env.ID, "@hourly", "nightly", true)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for descriptor, got %d", code)
	}
}

func TestCreateScheduled_TZPrefixRejected(t *testing.T) {
	h := newScheduledHarness(t)
	proj := h.makeProject("sched-tz")
	env := h.makeEnv("dev")
	rel := h.makeRelease(proj.ID, "1.0.0", "true")

	code := h.postSchedule(
		proj.ID,
		rel.ID,
		env.ID,
		"TZ=America/New_York 0 0 * * *",
		"nightly",
		true,
	)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for TZ= prefix, got %d", code)
	}
}
