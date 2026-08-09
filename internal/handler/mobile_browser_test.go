//go:build mobilebrowser

package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

type mobileBrowserFixtures struct {
	repo        *repository.Repository
	project     db.Project
	lifecycle   db.Lifecycle
	steps       []db.Step
	template    db.StepTemplate
	schedule    db.ScheduledDeployment
	variable    db.Variable
	secretVar   db.Variable
	sessions    map[string]*authedSession
	serverURL   string
	evidenceDir string
}

func newMobileBrowserFixtures(t *testing.T) mobileBrowserFixtures {
	t.Helper()
	ctx := context.Background()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	repo := repository.New(conn)
	broker := runner.NewLogBroker()
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	srv := httptest.NewServer(
		server.NewRouter(
			repo,
			runner.New(repo, broker),
			parser,
			handler.NewAuthHandler(repo),
		),
	)
	t.Cleanup(srv.Close)

	project := mustCreateProject(t, ctx, repo)
	environment := mustCreateEnvironment(t, ctx, repo)
	lifecycle := mustCreateLifecycle(t, ctx, repo, environment)
	steps := mustCreateMobileSteps(t, ctx, repo, project)
	release := mustCreateRelease(t, ctx, repo, project)
	template, err := repo.Queries.CreateStepTemplate(
		ctx,
		db.CreateStepTemplateParams{
			Name:       "hostile-template",
			ScriptBody: "echo " + strings.Repeat("template-token", 24),
		},
	)
	if err != nil {
		t.Fatalf("create step template: %v", err)
	}
	if _, err := repo.Queries.CreateStepTemplateVersion(
		ctx,
		db.CreateStepTemplateVersionParams{
			TemplateID:    template.ID,
			VersionNumber: 1,
			Name:          template.Name,
			ScriptBody:    template.ScriptBody,
		},
	); err != nil {
		t.Fatalf("create step template version: %v", err)
	}
	if _, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "succeeded",
		},
	); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	schedule := mustCreateSchedule(t, ctx, repo, project, release, environment)
	variable := mustCreateVariable(t, ctx, repo, project, environment)
	secretVar := mustCreateSecretVariable(t, ctx, repo, project)
	sessions := map[string]*authedSession{
		"admin":    seedSession(t, repo, srv.URL, "admin"),
		"deployer": seedSession(t, repo, srv.URL, "deployer"),
		"viewer":   seedSession(t, repo, srv.URL, "viewer"),
	}
	for _, role := range []string{"deployer", "viewer"} {
		if err := repo.Queries.AddProjectMember(
			ctx,
			db.AddProjectMemberParams{
				ProjectID: project.ID,
				UserID:    sessions[role].user.ID,
				Role:      "deployer",
			},
		); err != nil {
			t.Fatalf("add %s project membership: %v", role, err)
		}
	}
	if _, err := repo.Queries.CreateAuditLog(
		ctx,
		db.CreateAuditLogParams{
			UserID: sql.NullInt64{
				Int64: sessions["admin"].user.ID,
				Valid: true,
			},
			Action:     "hostile.audit",
			EntityType: "template",
			Details: sql.NullString{
				String: "{\"detail\":\"" + strings.Repeat(
					"audit-token",
					24,
				) + "\"}",
				Valid: true,
			},
		},
	); err != nil {
		t.Fatalf("create audit log: %v", err)
	}
	evidenceDir := t.TempDir()
	if configuredDir := os.Getenv("MOBILE_EVIDENCE_DIR"); configuredDir != "" {
		if err := os.MkdirAll(configuredDir, 0o700); err != nil {
			t.Fatalf("create mobile evidence directory: %v", err)
		}
		evidenceDir = configuredDir
	}
	return mobileBrowserFixtures{
		repo:        repo,
		project:     project,
		lifecycle:   lifecycle,
		steps:       steps,
		template:    template,
		schedule:    schedule,
		variable:    variable,
		secretVar:   secretVar,
		sessions:    sessions,
		serverURL:   srv.URL,
		evidenceDir: evidenceDir,
	}
}

func viewerWriteIsRejected(t *testing.T, fixtures mobileBrowserFixtures) {
	t.Helper()
	viewer := fixtures.sessions["viewer"]
	values := url.Values{
		"name":        {"blocked-step"},
		"script_body": {"echo blocked"},
		"csrf_token":  {viewer.csrfToken},
	}
	endpoint := fixtures.serverURL +
		fmt.Sprintf("/projects/%d/steps", fixtures.project.ID)
	req, err := http.NewRequest(
		http.MethodPost,
		endpoint,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatalf("viewer request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: viewer.sessionToken})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("viewer POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"viewer POST status = %d, want %d",
			resp.StatusCode,
			http.StatusForbidden,
		)
	}
	steps, err := fixtures.repo.Queries.ListStepsByProject(
		context.Background(),
		fixtures.project.ID,
	)
	if err != nil {
		t.Fatalf("list steps after viewer POST: %v", err)
	}
	if len(steps) != len(fixtures.steps) {
		t.Fatalf(
			"viewer POST created %d steps, want %d",
			len(steps),
			len(fixtures.steps),
		)
	}
}

func mobileBrowserEnvironment(
	fixtures mobileBrowserFixtures,
	role string,
	session *authedSession,
) []string {
	return []string{
		"BASE_URL=" + fixtures.serverURL,
		"MOBILE_ROLE=" + role,
		"MOBILE_COOKIE_NAME=session",
		"MOBILE_COOKIE_VALUE=" + session.sessionToken,
		fmt.Sprintf("MOBILE_PROJECT_ID=%d", fixtures.project.ID),
		fmt.Sprintf("MOBILE_LIFECYCLE_ID=%d", fixtures.lifecycle.ID),
		fmt.Sprintf("MOBILE_STEP_ID=%d", fixtures.steps[0].ID),
		fmt.Sprintf("MOBILE_TEMPLATE_ID=%d", fixtures.template.ID),
		fmt.Sprintf("MOBILE_SCHEDULE_ID=%d", fixtures.schedule.ID),
		fmt.Sprintf("MOBILE_VARIABLE_ID=%d", fixtures.variable.ID),
		fmt.Sprintf("MOBILE_SECRET_VARIABLE_ID=%d", fixtures.secretVar.ID),
		"MOBILE_SECRET_SENTINEL=" + mobileSecretSentinel,
		"MOBILE_OUTPUT_DIR=" + fixtures.evidenceDir,
		"MOBILE_STRICT=1",
	}
}
