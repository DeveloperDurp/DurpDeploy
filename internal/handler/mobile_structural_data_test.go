package handler_test

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const mobileStructuralSecret = "MOBILE_STRUCTURAL_SECRET_2d08d6b6"

type mobileStructuralData struct {
	project      db.Project
	lifecycle    db.Lifecycle
	deployment   db.Deployment
	step         db.Step
	stage        db.LifecycleStage
	template     db.StepTemplate
	schedule     db.ScheduledDeployment
	variable     db.Variable
	notification db.NotificationEvent
	auditDetails string
	admin        *authedSession
	viewer       *authedSession
}

func newMobileStructuralData(
	t *testing.T,
	repo *repository.Repository,
	serverURL string,
) mobileStructuralData {
	t.Helper()

	ctx := context.Background()
	project := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateProject(
			ctx,
			db.CreateProjectParams{Name: "mobile-structural-project"},
		)),
	)
	environment := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateEnvironment(
			ctx,
			db.CreateEnvironmentParams{Name: "mobile-structural-environment"},
		)),
	)
	lifecycle := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateLifecycle(
			ctx,
			db.CreateLifecycleParams{Name: "mobile-structural-lifecycle"},
		)),
	)
	stage := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateLifecycleStage(
			ctx,
			db.CreateLifecycleStageParams{
				LifecycleID:      lifecycle.ID,
				EnvironmentID:    environment.ID,
				SortOrder:        1,
				RequiresApproval: 1,
			},
		)),
	)
	mustMobileStructuralErr(
		t,
		repo.Queries.SetProjectLifecycle(
			ctx,
			db.SetProjectLifecycleParams{
				LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true},
				ID:          project.ID,
			},
		),
	)
	step := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateStep(
			ctx,
			db.CreateStepParams{
				ProjectID:  project.ID,
				Name:       "mobile-structural-step",
				ScriptBody: "printf structural-step",
				SortOrder:  1,
			},
		)),
	)
	release := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateRelease(
			ctx,
			db.CreateReleaseParams{
				ProjectID: project.ID,
				Version:   "mobile-structural-release",
				StepsJson: `[{"name":"deploy","script_body":"echo deploy","sort_order":1}]`,
			},
		)),
	)
	deployment := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID:     release.ID,
				EnvironmentID: environment.ID,
				Status:        "succeeded",
			},
		)),
	)
	template := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateStepTemplate(
			ctx,
			db.CreateStepTemplateParams{
				Name:       "mobile-structural-template",
				ScriptBody: "printf structural-template",
			},
		)),
	)
	mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateStepTemplateVersion(
			ctx,
			db.CreateStepTemplateVersionParams{
				TemplateID:    template.ID,
				VersionNumber: 1,
				Name:          template.Name,
				ScriptBody:    template.ScriptBody,
			},
		)),
	)
	schedule := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateScheduledDeployment(
			ctx,
			db.CreateScheduledDeploymentParams{
				ProjectID:     project.ID,
				ReleaseID:     release.ID,
				EnvironmentID: environment.ID,
				Cron:          "0 0 * * *",
				NextRunAt:     1,
				Enabled:       1,
			},
		)),
	)
	variable := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.CreateVariable(
			ctx,
			db.CreateVariableParams{
				ProjectID: project.ID,
				Name:      "MOBILE_STRUCTURAL_VALUE",
				Value: sql.NullString{
					String: "structural-value",
					Valid:  true,
				},
			},
		)),
	)
	mustMobileStructural(
		t,
		mobileStructuralQuery(repo.CreateVariable(
			ctx,
			db.CreateVariableParams{
				ProjectID: project.ID,
				Name:      "MOBILE_STRUCTURAL_SECRET",
				Value: sql.NullString{
					String: mobileStructuralSecret,
					Valid:  true,
				},
				EnvironmentID: sql.NullInt64{
					Int64: environment.ID,
					Valid: true,
				},
				Secret: 1,
			},
		)),
	)
	auditDetails := "mobile structural audit"
	mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateAuditLog(
			ctx,
			db.CreateAuditLogParams{
				Action:     "mobile.structural",
				EntityType: "project",
				Details:    sql.NullString{String: auditDetails, Valid: true},
			},
		)),
	)
	notification := mustMobileStructural(
		t,
		mobileStructuralQuery(repo.Queries.CreateNotificationEvent(
			ctx,
			db.CreateNotificationEventParams{
				EventType: "mobile.structural",
				ProjectID: sql.NullInt64{Int64: project.ID, Valid: true},
				EnvironmentID: sql.NullInt64{
					Int64: environment.ID,
					Valid: true,
				},
				Message: "mobile structural notification",
				Results: `{"email":"sent"}`,
			},
		)),
	)
	admin := seedSession(t, repo, serverURL, "admin")
	viewer := seedSession(t, repo, serverURL, "viewer")
	mustMobileStructuralErr(
		t,
		repo.Queries.AddProjectMember(
			ctx,
			db.AddProjectMemberParams{
				ProjectID: project.ID,
				UserID:    viewer.user.ID,
				Role:      "deployer",
			},
		),
	)

	return mobileStructuralData{
		project:      project,
		lifecycle:    lifecycle,
		deployment:   deployment,
		step:         step,
		stage:        stage,
		template:     template,
		schedule:     schedule,
		variable:     variable,
		notification: notification,
		auditDetails: auditDetails,
		admin:        admin,
		viewer:       viewer,
	}
}
