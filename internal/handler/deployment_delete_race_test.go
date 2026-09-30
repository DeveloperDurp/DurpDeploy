package handler_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentReturnsNotFoundWhenReleaseDisappearsAtLock(t *testing.T) {
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(t.Context(),
		db.CreateProjectParams{Name: "deleted-release"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := h.repo.Queries.CreateEnvironment(t.Context(),
		db.CreateEnvironmentParams{Name: "deleted-release"})
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.repo.Queries.CreateRelease(t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		})
	if err != nil {
		t.Fatal(err)
	}
	source, err := h.repo.Queries.CreateDeployment(t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "failed",
		})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a release lost after the handler lookup, at the shared lock.
	if _, err := h.repo.DB.Exec(`CREATE TRIGGER disappear_at_release_lock
BEFORE UPDATE OF version ON releases BEGIN
    SELECT RAISE(IGNORE);
END`); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ path, body string }{
		{fmt.Sprintf("/projects/%d/deploy", project.ID),
			fmt.Sprintf("release_id=%d&environment_id=%d", release.ID, env.ID)},
		{fmt.Sprintf("/deployments/%d/redeploy", source.ID), ""},
		{fmt.Sprintf("/projects/%d/releases/%d/refresh", project.ID, release.ID), ""},
	} {
		t.Run(test.path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost,
				h.server.URL+test.path, strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-CSRF-Token", h.csrfToken())
			response, err := h.authedClient().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status=%d", response.StatusCode)
			}
		})
	}
}
