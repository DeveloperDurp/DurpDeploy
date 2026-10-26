//go:build e2e

package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"
	"github.com/robfig/cron/v3"
)

func TestReleaseCaptureConsistentAPIWebE2E(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			dsn := projectUpdateDSN(t, backend)
			base, err := migrate.Run(dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { testutil.Close(t, base) })
			conn := sql.OpenDB(
				releaseCaptureConnector{upstream: base.Driver(), dsn: dsn},
			)
			t.Cleanup(func() { testutil.Close(t, conn) })
			repo := repository.New(conn)
			seedReleaseCaptureAgent(t, repo)
			for _, surface := range []string{"API", "Web"} {
				for _, action := range []string{"create", "refresh"} {
					for _, edit := range []string{"selectors", "delete-selectors", "target"} {
						t.Run(surface+"/"+action+"/"+edit, func(t *testing.T) {
							testReleaseCapture(
								t,
								repo,
								backend,
								surface,
								action,
								edit,
							)
						})
					}
				}
			}
		})
	}
}

func testReleaseCapture(
	t *testing.T,
	repo *repository.Repository,
	backend, surface, action, edit string,
) {
	t.Helper()
	barrier := &releaseCaptureBarrier{
		read:   make(chan struct{}),
		edit:   make(chan struct{}),
		resume: make(chan struct{}),
	}
	var resume sync.Once
	defer resume.Do(func() { close(barrier.resume) })
	rnr := runner.NewWithPodmanBinaryForTest(
		repo,
		runner.NewLogBroker(),
		setupTestPodman(t),
	)
	t.Cleanup(rnr.KillAll)
	app := server.NewRouter(
		repo,
		rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(repo),
	)
	srv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if operation := r.Header.Get("X-Capture-Test"); operation != "" {
				r = r.WithContext(
					context.WithValue(
						r.Context(),
						releaseCaptureKey{},
						releaseCaptureOperation{
							barrier: barrier,
							edit:    operation == "edit",
						},
					),
				)
			}
			app.ServeHTTP(w, r)
		}),
	)
	t.Cleanup(srv.Close)
	h := &projectHarness{
		t:      t,
		repo:   repo,
		server: srv,
		sess: seedSessionAs(
			t,
			repo,
			srv.URL,
			"capture-"+surface+"-"+action+"-"+edit+"@example.test",
			"admin",
		),
	}
	h.authedClient().Timeout = 30 * time.Second
	owner := accountUpdateToken(t, repo, h.sess.user.ID)
	project := h.makeProject("capture-" + surface + "-" + action + "-" + edit)
	step, err := repo.CreateStepWithPlacement(
		t.Context(),
		db.CreateStepParams{
			ProjectID:          project.ID,
			Name:               "Capture",
			ScriptBody:         "echo old",
			Interpreter:        "bash",
			AgentExecutionMode: "host",
		},
		"agent",
		[]string{"old-agent"},
	)
	if err != nil {
		t.Fatal(err)
	}
	apiPath, path, want := h.releaseCapturePath(t, surface, action, project.ID)
	var payload map[string]any
	if action == "create" {
		payload = map[string]any{"version": "v1"}
	}
	capture := releaseCaptureRequest(
		t,
		h,
		owner,
		"POST",
		path,
		payload,
		surface == "Web",
	)
	capture.Header.Set("X-Capture-Test", "capture")
	result := make(chan releaseCaptureResponse, 1)
	go func() { result <- doReleaseCaptureRequest(h.authedClient(), capture) }()
	awaitCaptureSignal(t, barrier.read)
	selectors, target, image := []string{"new-agent"}, "agent", ""
	if edit == "delete-selectors" {
		selectors = []string{}
	} else if edit == "target" {
		selectors, target, image = []string{}, "local", "harbor.durp.info/dockerhub/library/bash:5.2"
	}
	mutation := releaseCaptureRequest(
		t,
		h,
		owner,
		"PUT",
		fmt.Sprintf("/api/v1/projects/%d/steps/%d", project.ID, step.ID),
		map[string]any{
			"name":                 step.Name,
			"script_body":          "echo new",
			"interpreter":          "bash",
			"agent_execution_mode": "host",
			"execution_target":     target,
			"agent_selectors":      selectors,
			"container_image":      image,
		},
		false,
	)
	mutation.Header.Set("X-Capture-Test", "edit")
	changed := make(chan releaseCaptureResponse, 1)
	go func() { changed <- doReleaseCaptureRequest(h.authedClient(), mutation) }()
	awaitCaptureSignal(t, barrier.edit)
	if backend == "SQLServer" {
		select {
		case response := <-changed:
			t.Fatalf("writer passed the source lock: %+v", response)
		case <-time.After(100 * time.Millisecond):
		}
	} else {
		assertCaptureResponse(t, <-changed, 200)
	}
	resume.Do(func() { close(barrier.resume) })
	assertCaptureResponse(t, <-result, want)
	if backend == "SQLServer" {
		assertCaptureResponse(t, <-changed, 200)
	}
	releases, err := repo.Queries.ListReleasesByProject(t.Context(), project.ID)
	if err != nil || len(releases) != 1 {
		t.Fatalf("captured releases: %v: %v", releases, err)
	}
	releaseID := releases[0].ID
	h.assertCapturedSource(
		t,
		owner,
		apiPath,
		releaseID,
		"echo old",
		"agent",
		[]string{"old-agent"},
	)
	// A later refresh must see the complete committed new source.
	refresh := releaseCaptureRequest(
		t,
		h,
		owner,
		"POST",
		fmt.Sprintf("%s/%d/refresh", apiPath, releaseID),
		nil,
		false,
	)
	assertCaptureResponse(
		t,
		doReleaseCaptureRequest(h.authedClient(), refresh),
		200,
	)
	h.assertCapturedSource(
		t,
		owner,
		apiPath,
		releaseID,
		"echo new",
		target,
		selectors,
	)
}

func seedReleaseCaptureAgent(t *testing.T, repo *repository.Repository) {
	t.Helper()
	if _, err := repo.Queries.CreateAgent(t.Context(), db.CreateAgentParams{ID: "capture-agent", Name: "capture-agent", Endpoint: "https://agent.test"}); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"old-agent", "new-agent"} {
		if _, err := repo.Queries.AddAgentLabel(t.Context(), db.AddAgentLabelParams{AgentID: "capture-agent", Label: label}); err != nil {
			t.Fatal(err)
		}
	}
}

func (h *projectHarness) releaseCapturePath(
	t *testing.T,
	surface, action string,
	projectID int64,
) (string, string, int) {
	t.Helper()
	repo := h.repo
	apiPath := fmt.Sprintf("/api/v1/projects/%d/releases", projectID)
	path, want := apiPath, 201
	if surface == "Web" {
		path, want = fmt.Sprintf("/projects/%d/releases", projectID), 303
	}
	if action == "refresh" {
		if _, err := handler.CreateReleaseSnapshot(t.Context(), repo, projectID, "v1"); err != nil {
			t.Fatal(err)
		}
		releases, err := repo.Queries.ListReleasesByProject(
			t.Context(),
			projectID,
		)
		if err != nil || len(releases) != 1 {
			t.Fatalf("seed release: %v: %v", releases, err)
		}
		path += fmt.Sprintf("/%d/refresh", releases[0].ID)
		if surface == "API" {
			want = 200
		}
	}
	return apiPath, path, want
}

type releaseCaptureResponse struct {
	status int
	body   []byte
	err    error
}

func doReleaseCaptureRequest(
	client *http.Client,
	request *http.Request,
) releaseCaptureResponse {
	response, err := client.Do(request)
	if err != nil {
		return releaseCaptureResponse{err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return releaseCaptureResponse{
		status: response.StatusCode,
		body:   body,
		err:    err,
	}
}

func assertCaptureResponse(
	t *testing.T,
	response releaseCaptureResponse,
	want int,
) {
	t.Helper()
	if response.err != nil || response.status != want {
		t.Fatalf(
			"response=%d, want=%d, error=%v, body=%s",
			response.status,
			want,
			response.err,
			response.body,
		)
	}
}

func releaseCaptureRequest(
	t *testing.T,
	h *projectHarness,
	token, method, path string,
	payload map[string]any,
	web bool,
) *http.Request {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	if web {
		body = []byte(
			url.Values{
				"version":    {"v1"},
				"csrf_token": {h.csrfToken()},
			}.Encode(),
		)
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		method,
		h.server.URL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	if web {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("X-CSRF-Token", h.csrfToken())
	}
	return request
}

func (h *projectHarness) assertCapturedSource(
	t *testing.T,
	token, apiPath string,
	id int64,
	script, target string,
	selectors []string,
) {
	t.Helper()
	request := releaseCaptureRequest(
		t,
		h,
		token,
		"GET",
		fmt.Sprintf("%s/%d", apiPath, id),
		nil,
		false,
	)
	response := doReleaseCaptureRequest(h.authedClient(), request)
	assertCaptureResponse(t, response, 200)
	var release struct {
		StepsJSON string `json:"steps_json"`
	}
	if err := json.Unmarshal(response.body, &release); err != nil {
		t.Fatal(err)
	}
	var steps []struct {
		Script    string   `json:"script_body"`
		Target    string   `json:"execution_target"`
		Selectors []string `json:"agent_selectors"`
	}
	if err := json.Unmarshal([]byte(release.StepsJSON), &steps); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Script != script ||
		steps[0].Target != target ||
		!slices.Equal(steps[0].Selectors, selectors) {
		t.Fatalf(
			"mixed source snapshot: %+v; want %s / %s / %v",
			steps,
			script,
			target,
			selectors,
		)
	}
}

func awaitCaptureSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal("capture barrier was not reached")
	}
}
