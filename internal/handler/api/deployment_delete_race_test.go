package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

func TestDeploymentReturnsNotFoundWhenReleaseDisappearsAtLock(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "deleted-release@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	env := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	source, err := h.repo.Queries.CreateDeployment(t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "failed",
		})
	if err != nil {
		t.Fatal(err)
	}
	// Fault injection: the preliminary lookup succeeds, but the transaction's
	// lock sees zero rows, as it does after a concurrent delete wins.
	if _, err := h.repo.DB.Exec(`CREATE TRIGGER disappear_at_release_lock
BEFORE UPDATE OF version ON releases BEGIN
    SELECT RAISE(IGNORE);
END`); err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	for _, test := range []struct{ path, body string }{
		{fmt.Sprintf("/api/v1/projects/%d/deployments", project.ID),
			fmt.Sprintf(`{"release_id":%d,"environment_id":%d}`, release.ID, env.ID)},
		{fmt.Sprintf("/api/v1/deployments/%d/redeploy", source.ID), "{}"},
		{fmt.Sprintf("/api/v1/deployments/%d/retry", source.ID), "{}"},
		{fmt.Sprintf("/api/v1/projects/%d/releases/%d/refresh",
			project.ID, release.ID), "{}"},
	} {
		t.Run(test.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, test.path,
				strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusNotFound {
				t.Fatalf(
					"status=%d body=%s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}
