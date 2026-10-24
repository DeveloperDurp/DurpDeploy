//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"

	"github.com/robfig/cron/v3"
)

func TestTemplateLibrarySQLServerCaseConflictE2E(t *testing.T) {
	conn, err := migrate.Run(testutil.SQLServerDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, conn) })
	repo := repository.New(conn)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	user := seedAPIUser(t, repo, "library-case@example.test", "admin")
	_, token := seedAPIToken(t, repo, user.ID)
	project := seedProject(t, repo)
	original, err := repo.Queries.CreateVariable(
		t.Context(),
		db.CreateVariableParams{
			ProjectID: project.ID, Name: "tf_http_password",
			Value: sql.NullString{String: "existing", Valid: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rnr := runner.New(repo, runner.NewLogBroker())
	srv := httptest.NewServer(server.NewRouter(repo, rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Month|cron.Dom|cron.Dow),
		handler.NewAuthHandler(repo)))
	t.Cleanup(srv.Close)
	f := permissionFixture{server: srv}
	entry, err := repository.GetLibraryTemplate("terraform-plan-apply")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(libraryInput(entry))
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/projects/%d/template-library/%s/apply",
		project.ID, entry.Slug)
	response := f.request(t, "POST", path, string(body), token, "", false, 409)
	if strings.Contains(string(response), "library-fresh-secret") ||
		strings.Contains(string(response), "UNIQUE") {
		t.Fatal("conflict response disclosed configuration or database details")
	}
	form := url.Values{"version": {entry.Version}, "reviewed": {"true"}}
	for name, value := range libraryInput(entry).VariableValues {
		form.Set("variable_"+name, value)
	}
	session := memberRoleSession(t, &harness{repo: repo}, user.ID)
	page := f.request(t, "POST", strings.TrimPrefix(path, "/api/v1"),
		form.Encode(), "", session, false, 409)
	if strings.Contains(string(page), "library-fresh-secret") ||
		strings.Contains(string(page), "UNIQUE") {
		t.Fatal("web conflict disclosed configuration or database details")
	}
	variables, err := repo.Queries.ListVariablesByProject(
		t.Context(),
		project.ID,
	)
	if err != nil || len(variables) != 1 || variables[0] != original {
		t.Fatalf("conflict changed existing variables: %v, %v", variables, err)
	}
	steps, err := repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatalf("conflict retained partial steps: %v, %v", steps, err)
	}
}
