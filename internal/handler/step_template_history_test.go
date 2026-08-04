package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

// stepTemplateHarness mounts the step-template routes on an httptest server
// and exposes form-encoded POST/PUT/DELETE/GET helpers.
type stepTemplateHarness struct {
	t      *testing.T
	repo   *repository.Repository
	server *httptest.Server
	client *http.Client
}

func newStepTemplateHarness(t *testing.T) *stepTemplateHarness {
	t.Helper()

	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = dbConn.Close() })

	repo := repository.New(dbConn)
	sth := handler.NewStepTemplateHandler(repo)

	r := chi.NewRouter()
	r.Get("/templates", sth.ListTemplates)
	r.Post("/templates", sth.CreateTemplate)
	r.Put("/templates/{id}", sth.UpdateTemplate)
	r.Delete("/templates/{id}", sth.DeleteTemplate)
	r.Get("/templates/{id}/history", sth.ListTemplateHistory)
	r.Post(
		"/projects/{id}/steps/{stepId}/save-as-template",
		sth.SaveStepAsTemplate,
	)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &stepTemplateHarness{
		t:      t,
		repo:   repo,
		server: srv,
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func TestStepTemplate_SaveRejectsStepFromAnotherProject(t *testing.T) {
	h := newStepTemplateHarness(t)
	ctx := context.Background()

	projectA, err := h.repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "project-a"},
	)
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projectB, err := h.repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "project-b"},
	)
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}
	step, err := h.repo.Queries.CreateStep(
		ctx,
		db.CreateStepParams{
			ProjectID:  projectB.ID,
			Name:       "private-step",
			ScriptBody: "echo project-b-secret",
		},
	)
	if err != nil {
		t.Fatalf("create project B step: %v", err)
	}

	resp, err := h.client.Post(
		fmt.Sprintf(
			"%s/projects/%d/steps/%d/save-as-template",
			h.server.URL,
			projectA.ID,
			step.ID,
		),
		"application/x-www-form-urlencoded",
		nil,
	)
	if err != nil {
		t.Fatalf("save cross-project step as template: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	templates, err := h.repo.Queries.ListStepTemplates(ctx)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(templates) != 0 {
		t.Fatalf("expected no copied templates, got %d", len(templates))
	}
}

func (h *stepTemplateHarness) createTemplate(name, script string) int {
	h.t.Helper()
	form := url.Values{}
	form.Set("name", name)
	form.Set("script_body", script)
	resp, err := h.client.PostForm(
		h.server.URL+"/templates",
		form,
	)
	if err != nil {
		h.t.Fatalf("POST /templates: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *stepTemplateHarness) updateTemplate(
	id int64, name, script string,
) int {
	h.t.Helper()
	form := url.Values{}
	form.Set("name", name)
	form.Set("script_body", script)
	req, _ := http.NewRequest(
		"PUT",
		fmt.Sprintf("%s/templates/%d", h.server.URL, id),
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("PUT /templates/%d: %v", id, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *stepTemplateHarness) deleteTemplate(id int64) int {
	h.t.Helper()
	req, _ := http.NewRequest(
		"DELETE",
		fmt.Sprintf("%s/templates/%d", h.server.URL, id),
		nil,
	)
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("DELETE /templates/%d: %v", id, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func (h *stepTemplateHarness) getHistory(id int64) (int, string) {
	h.t.Helper()
	resp, err := h.client.Get(
		fmt.Sprintf("%s/templates/%d/history", h.server.URL, id),
	)
	if err != nil {
		h.t.Fatalf("GET /templates/%d/history: %v", id, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatalf("read history body: %v", err)
	}
	return resp.StatusCode, string(raw)
}

func (h *stepTemplateHarness) templateID(name string) int64 {
	h.t.Helper()
	templates, err := h.repo.Queries.ListStepTemplates(
		context.Background(),
	)
	if err != nil {
		h.t.Fatalf("list templates: %v", err)
	}
	for _, tpl := range templates {
		if tpl.Name == name {
			return tpl.ID
		}
	}
	h.t.Fatalf("template %q not found", name)
	return 0
}

func TestStepTemplate_VersioningShadowHistory(t *testing.T) {
	h := newStepTemplateHarness(t)
	ctx := context.Background()

	// 1. Create: expect 303 + exactly 1 version (v1).
	if code := h.createTemplate("T1", "echo 1"); code != http.StatusSeeOther {
		t.Fatalf("create T1: expected 303, got %d", code)
	}
	id := h.templateID("T1")
	versions, err := h.repo.Queries.ListStepTemplateVersions(ctx, id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf(
			"after create: expected 1 version, got %d",
			len(versions),
		)
	}
	if versions[0].VersionNumber != 1 {
		t.Fatalf("v1 number: got %d", versions[0].VersionNumber)
	}
	if versions[0].ScriptBody != "echo 1" {
		t.Fatalf("v1 script: got %q", versions[0].ScriptBody)
	}

	// 2. Update to "echo 2": expect 2 versions, newest first.
	if code := h.updateTemplate(
		id,
		"T1",
		"echo 2",
	); code != http.StatusSeeOther {
		t.Fatalf("update T1->echo 2: expected 303, got %d", code)
	}
	versions, err = h.repo.Queries.ListStepTemplateVersions(ctx, id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("after 1st update: expected 2 versions, got %d", len(versions))
	}
	if versions[0].VersionNumber != 2 || versions[0].ScriptBody != "echo 2" {
		t.Fatalf(
			"newest version: got number=%d body=%q",
			versions[0].VersionNumber,
			versions[0].ScriptBody,
		)
	}
	if versions[1].VersionNumber != 1 || versions[1].ScriptBody != "echo 1" {
		t.Fatalf(
			"oldest version: got number=%d body=%q",
			versions[1].VersionNumber,
			versions[1].ScriptBody,
		)
	}

	// 3. Update to "echo 3": expect 3 versions.
	if code := h.updateTemplate(
		id,
		"T1",
		"echo 3",
	); code != http.StatusSeeOther {
		t.Fatalf("update T1->echo 3: expected 303, got %d", code)
	}
	versions, err = h.repo.Queries.ListStepTemplateVersions(ctx, id)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("after 2nd update: expected 3 versions, got %d", len(versions))
	}

	// 4. GET /templates/{id}/history.
	code, body := h.getHistory(id)
	if code != http.StatusOK {
		t.Fatalf("GET history: expected 200, got %d (body=%q)", code, body)
	}
	if !strings.Contains(body, "v3") {
		t.Errorf("history body missing %q\nbody=%s", "v3", body)
	}
	echo3Idx := strings.Index(body, "echo 3")
	echo1Idx := strings.Index(body, "echo 1")
	if echo3Idx < 0 || echo1Idx < 0 {
		t.Fatalf(
			"history body missing echo markers (echo3=%d, echo1=%d)\nbody=%s",
			echo3Idx,
			echo1Idx,
			body,
		)
	}
	if echo3Idx > echo1Idx {
		t.Errorf(
			"newest first violated: echo 3 (idx %d) must appear before echo 1 (idx %d)",
			echo3Idx,
			echo1Idx,
		)
	}
	if !strings.Contains(body, "T1") {
		t.Errorf("history body missing template name %q\nbody=%s", "T1", body)
	}

	// 5. Delete the template; CASCADE should drop versions. History -> 404.
	if code := h.deleteTemplate(id); code != http.StatusSeeOther {
		t.Fatalf("delete T1: expected 303, got %d", code)
	}
	if remaining, err := h.repo.Queries.ListStepTemplateVersions(
		ctx,
		id,
	); err != nil {
		t.Fatalf("list versions after delete: %v", err)
	} else if len(
		remaining,
	) != 0 {
		t.Fatalf(
			"CASCADE failed: expected 0 versions after delete, got %d",
			len(remaining),
		)
	}
	if code, body := h.getHistory(id); code != http.StatusNotFound {
		t.Errorf(
			"GET history after delete: expected 404, got %d (body=%q)",
			code,
			body,
		)
	}
}
