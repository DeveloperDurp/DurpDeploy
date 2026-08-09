package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

type mobileStructuralFixture struct {
	serverURL    string
	repo         *repository.Repository
	project      db.Project
	lifecycle    db.Lifecycle
	deployment   db.Deployment
	step         db.Step
	secondStep   db.Step
	stage        db.LifecycleStage
	secondStage  db.LifecycleStage
	template     db.StepTemplate
	schedule     db.ScheduledDeployment
	variable     db.Variable
	notification db.NotificationEvent
	auditDetails string
	admin        *authedSession
	deployer     *authedSession
	viewer       *authedSession
}

func newMobileStructuralFixture(t *testing.T) mobileStructuralFixture {
	t.Helper()

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
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	srv := httptest.NewServer(
		server.NewRouter(
			repo,
			runner.New(repo, runner.NewLogBroker()),
			parser,
			handler.NewAuthHandler(repo),
		),
	)
	t.Cleanup(srv.Close)

	data := newMobileStructuralData(t, repo, srv.URL)
	return mobileStructuralFixture{
		serverURL:    srv.URL,
		repo:         repo,
		project:      data.project,
		lifecycle:    data.lifecycle,
		deployment:   data.deployment,
		step:         data.step,
		stage:        data.stage,
		template:     data.template,
		schedule:     data.schedule,
		variable:     data.variable,
		notification: data.notification,
		auditDetails: data.auditDetails,
		admin:        data.admin,
		viewer:       data.viewer,
	}
}

func (f mobileStructuralFixture) withWriterControls(
	t *testing.T,
) mobileStructuralFixture {
	t.Helper()

	ctx := context.Background()
	secondEnvironment := mustMobileStructural(
		t,
		mobileStructuralQuery(f.repo.Queries.CreateEnvironment(
			ctx,
			db.CreateEnvironmentParams{
				Name: "mobile-structural-second-environment",
			},
		)),
	)
	secondStage := mustMobileStructural(
		t,
		mobileStructuralQuery(f.repo.Queries.CreateLifecycleStage(
			ctx,
			db.CreateLifecycleStageParams{
				LifecycleID:   f.lifecycle.ID,
				EnvironmentID: secondEnvironment.ID,
				SortOrder:     2,
			},
		)),
	)
	secondStep := mustMobileStructural(
		t,
		mobileStructuralQuery(f.repo.Queries.CreateStep(
			ctx,
			db.CreateStepParams{
				ProjectID:  f.project.ID,
				Name:       "mobile-structural-second-step",
				ScriptBody: "printf structural-second-step",
				SortOrder:  2,
			},
		)),
	)
	deployer := seedSession(t, f.repo, f.serverURL, "deployer")
	mustMobileStructuralErr(
		t,
		f.repo.Queries.AddProjectMember(
			ctx,
			db.AddProjectMemberParams{
				ProjectID: f.project.ID,
				UserID:    deployer.user.ID,
				Role:      "deployer",
			},
		),
	)

	f.secondStep = secondStep
	f.secondStage = secondStage
	f.deployer = deployer
	return f
}

func (f mobileStructuralFixture) getHTML(
	t *testing.T,
	session *authedSession,
	path string,
) string {
	t.Helper()

	resp, err := session.client.Get(f.serverURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET %s response: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200", path, resp.StatusCode)
	}
	return string(body)
}

type mobileStructuralResult[T any] struct {
	value T
	err   error
}

func mobileStructuralQuery[T any](
	value T,
	err error,
) mobileStructuralResult[T] {
	return mobileStructuralResult[T]{value: value, err: err}
}

func mustMobileStructural[T any](
	t *testing.T,
	result mobileStructuralResult[T],
) T {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	return result.value
}

func mustMobileStructuralErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
