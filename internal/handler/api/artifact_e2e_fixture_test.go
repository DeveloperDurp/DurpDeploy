//go:build e2e

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

type artifactE2E struct {
	h                    *harness
	server               *httptest.Server
	baseURL              string
	client               *http.Client
	token, session, csrf string
	project              db.Project
	environment          db.Environment
	upstream             string
	done                 chan events.Event
	changePackage        func(string)
	changeCredential     func(string)
	completionTimeout    time.Duration
}

type artifactNotifier struct{ done chan events.Event }

func (n artifactNotifier) Name() string { return "artifact-e2e" }

func (n artifactNotifier) Notify(
	_ context.Context,
	e events.Event,
) (bool, error) {
	switch e.Type {
	case events.DeploymentSucceeded,
		events.DeploymentFailed,
		events.RunbookSucceeded,
		events.RunbookFailed:
		n.done <- e
	}
	return true, nil
}

func newArtifactE2E(t *testing.T) *artifactE2E {
	t.Helper()
	if os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") == "" {
		kind := "docker"
		if _, err := exec.LookPath("podman"); err == nil {
			kind = "podman"
		}
		t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", kind)
	}
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "artifact-e2e-"+uuid.NewString())
	h := newAPIHarness(t)
	if !h.runner.ContainerRuntimeReady() {
		t.Fatal("a working container endpoint is required for artifact E2E")
	}
	t.Cleanup(h.runner.KillAll)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h.repo.SetSecretBox(box)
	user := seedAPIUser(t, h.repo, "artifact-e2e@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	session, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.Queries.CreateSession(
		t.Context(),
		db.CreateSessionParams{
			ID:        session,
			UserID:    user.ID,
			CsrfToken: csrf,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		},
	); err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(
		h.repo,
		h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo),
	)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	done := make(chan events.Event, 16)
	bus := events.NewBus(h.repo)
	bus.Register(artifactNotifier{done})
	h.runner.SetEventBus(bus)
	upstream, client, change, credential := artifactHTTPSFixture(t)
	workspace, err := artifact.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := workspace.Close(); err != nil {
			t.Error(err)
		}
	})
	client.TempDir = workspace.Directory
	h.repo.ArtifactClient = client
	return &artifactE2E{
		h:       h,
		server:  srv,
		baseURL: srv.URL,
		client: &http.Client{
			Timeout:       time.Minute,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		token:             token,
		session:           session,
		csrf:              csrf,
		project:           seedProject(t, h.repo),
		environment:       seedEnv(t, h.repo),
		upstream:          upstream,
		done:              done,
		changePackage:     change,
		changeCredential:  credential,
		completionTimeout: 30 * time.Second,
	}
}

func (f *artifactE2E) api(
	t *testing.T,
	method, path string,
	body any,
	want int,
) []byte {
	t.Helper()
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(
		t.Context(),
		method,
		f.baseURL+path,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf(
			"%s %s: status=%d want=%d body=%s",
			method,
			path,
			response.StatusCode,
			want,
			result,
		)
	}
	return result
}

func (f *artifactE2E) web(
	t *testing.T,
	method, path string,
	form url.Values,
	want int,
) string {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf_token", f.csrf)
	req, err := http.NewRequestWithContext(
		t.Context(),
		method,
		f.baseURL+path,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	result, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf(
			"web %s %s: status=%d body=%s",
			method,
			path,
			response.StatusCode,
			result,
		)
	}
	return string(result)
}

func (f *artifactE2E) completion(t *testing.T, id int64, want events.Type) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), f.completionTimeout)
	defer cancel()
	for {
		select {
		case event := <-f.done:
			if event.DeploymentID == id {
				if event.Type != want {
					t.Fatalf("completion=%s: %s", event.Type, event.Message)
				}
				return
			}
		case <-ctx.Done():
			t.Fatalf("deployment %d did not finish: %v", id, ctx.Err())
		}
	}
}

func (f *artifactE2E) base() string { return fmt.Sprintf("/api/v1/projects/%d", f.project.ID) }
