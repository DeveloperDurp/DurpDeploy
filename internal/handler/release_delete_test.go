package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestReleaseDeleteModalShowsActiveStateAndDeletesAfterCompletion(
	t *testing.T,
) {
	h := newHarness(t)
	ctx := context.Background()
	project, err := h.repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "delete-modal-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "delete-me", StepsJson: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := h.repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "delete-modal-env"})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := h.repo.Queries.CreateDeployment(ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
		})
	if err != nil {
		t.Fatal(err)
	}
	listPath := fmt.Sprintf("/projects/%d/releases", project.ID)
	path := fmt.Sprintf("%s/%d", listPath, release.ID)
	for _, page := range []string{listPath, path} {
		response, err := h.authedClient().Get(h.server.URL + page)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status=%d err=%v", page, response.StatusCode, err)
		}
		if !strings.Contains(
			string(body),
			"active or unconfirmed deployment",
		) ||
			strings.Contains(string(body), "hx-delete=\""+path+"\"") {
			t.Fatalf("GET %s: active modal is not blocked", page)
		}
	}
	req, err := http.NewRequest(http.MethodDelete, h.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	req.Header.Set("HX-Request", "true")
	response, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("active delete=%d", response.StatusCode)
	}
	if _, err := h.repo.DB.ExecContext(ctx,
		"UPDATE deployments SET status = 'succeeded' WHERE id = ?",
		deployment.ID); err != nil {
		t.Fatal(err)
	}
	response, err = h.authedClient().Get(h.server.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !strings.Contains(string(body),
		"hx-delete=\""+path+"\"") ||
		!strings.Contains(string(body), "deployment history, and logs") {
		t.Fatalf("terminal modal: status=%d err=%v", response.StatusCode, err)
	}
	response, err = h.authedClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		response.Header.Get("HX-Redirect") != listPath {
		t.Fatalf("delete: status=%d redirect=%s", response.StatusCode,
			response.Header.Get("HX-Redirect"))
	}
	response, err = h.authedClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("repeat delete=%d", response.StatusCode)
	}
	for _, badPath := range []string{
		"/projects/invalid/releases/1",
		fmt.Sprintf("/projects/%d/releases/invalid", project.ID),
	} {
		invalid, err := http.NewRequest(http.MethodDelete,
			h.server.URL+badPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		invalid.Header.Set("X-CSRF-Token", h.csrfToken())
		result, err := h.authedClient().Do(invalid)
		if err != nil {
			t.Fatal(err)
		}
		result.Body.Close()
		if result.StatusCode != http.StatusBadRequest {
			t.Fatalf("DELETE %s: status=%d", badPath, result.StatusCode)
		}
	}
}
