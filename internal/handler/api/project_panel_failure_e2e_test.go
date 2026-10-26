//go:build e2e

package api_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

// Fail only the selected read. Authentication and project access still use
// the migrated database through the normal router.
type projectPanelFailureDB struct {
	*sql.DB
	query atomic.Value
}

func (d *projectPanelFailureDB) fails(query string) bool {
	name, _ := d.query.Load().(string)
	return name != "" && strings.Contains(query, "-- name: "+name+" :")
}

func (d *projectPanelFailureDB) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	if d.fails(query) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return d.DB.QueryRowContext(cancelled, query, args...)
	}
	return d.DB.QueryRowContext(ctx, query, args...)
}

func (d *projectPanelFailureDB) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	if d.fails(query) {
		return nil, errors.New("injected deployment history failure")
	}
	return d.DB.QueryContext(ctx, query, args...)
}

func newProjectPanelFailureFixture(
	t *testing.T,
) (*artifactE2E, *projectPanelFailureDB) {
	t.Helper()
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "panel-failure@example.test", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	fault := &projectPanelFailureDB{DB: h.repo.DB}
	h.repo.Queries = db.New(fault)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	return &artifactE2E{
		h: h, baseURL: srv.URL,
		client: &http.Client{Timeout: 10 * time.Second},
		token:  token, session: memberRoleSession(t, h, user.ID),
		project: seedProject(t, h.repo), environment: seedEnv(t, h.repo),
	}, fault
}

func TestProjectPanelFailureAPIWebE2E(t *testing.T) {
	f, fault := newProjectPanelFailureFixture(t)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Panel failure lifecycle"}, 201), &lifecycle)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
		map[string]int64{"environment_id": f.environment.ID}, 201)
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	apiPath := "/api/v1" + path + "/deployments"
	for _, lifecycleID := range []int64{0, lifecycle.ID} {
		f.api(t, "PUT", f.base(), map[string]any{
			"name": f.project.Name, "lifecycle_id": lifecycleID,
		}, 200)
		for _, query := range []string{
			"ListProjectPanelDeployments",
			"ListEnvironments",
		} {
			t.Run(fmt.Sprintf("%d/%s", lifecycleID, query), func(t *testing.T) {
				f.checkProjectPanelFailure(t, fault, query, path, apiPath)
			})
		}
		var history struct {
			Items []db.Deployment `json:"items"`
			Total int             `json:"total"`
		}
		decodeLifecycleTest(t, f.api(t, "GET", apiPath, nil, 200), &history)
		if history.Total != 0 || len(history.Items) != 0 {
			t.Fatalf("empty history API: %+v", history)
		}
		if body := f.web(t, "GET", path, nil, 200); !strings.Contains(
			body,
			"No successful deploys",
		) {
			t.Fatal("an environment without deployments lost its empty state")
		}
	}
	fault.query.Store("ListDeploymentsWithRefsFiltered")
	f.api(t, "GET", apiPath, nil, 500)
	fault.query.Store("")
	f.api(t, "GET", apiPath, nil, 200)
}

func (f *artifactE2E) checkProjectPanelFailure(
	t *testing.T,
	fault *projectPanelFailureDB,
	query, path, apiPath string,
) {
	t.Helper()
	fault.query.Store(query)
	for _, page := range []string{"/projects", path} {
		body := f.web(t, "GET", page, nil, 500)
		if strings.Contains(body, "No deployments yet") ||
			strings.Contains(body, "No successful deploys") ||
			strings.Contains(body, "<!doctype") {
			t.Fatalf(
				"storage failure rendered an empty panel: %s",
				body,
			)
		}
	}
	// The equivalent public history API uses a separate query.
	// A panel-only failure must not break that independent read.
	f.api(t, "GET", apiPath, nil, 200)
	fault.query.Store("")
	for _, page := range []string{"/projects", path} {
		f.web(t, "GET", page, nil, 200)
	}
}
