package migrate

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

type mssqlDeploymentFixture struct {
	ctx           context.Context
	queries       *db.Queries
	projectID     int64
	environmentID int64
	deployment    db.Deployment
	userID        int64
}

func verifyMSSQLDeploymentQueries(
	t *testing.T,
	fixture mssqlDeploymentFixture,
) {
	t.Helper()

	notificationEvent, err := fixture.queries.CreateNotificationEvent(
		fixture.ctx,
		db.CreateNotificationEventParams{
			EventType: "deployment_started",
			DeploymentID: sql.NullInt64{
				Int64: fixture.deployment.ID,
				Valid: true,
			},
			ProjectID: sql.NullInt64{
				Int64: fixture.projectID,
				Valid: true,
			},
			EnvironmentID: sql.NullInt64{
				Int64: fixture.environmentID,
				Valid: true,
			},
			Message: "MSSQL notification event",
			Results: `{"test":"ok"}`,
		},
	)
	requireNoError(t, err, "create notification event")
	if notificationEvent.ID == 0 ||
		notificationEvent.EventType != "deployment_started" ||
		notificationEvent.Message != "MSSQL notification event" ||
		notificationEvent.Results != `{"test":"ok"}` ||
		notificationEvent.CreatedAt <= 0 {
		t.Fatalf("created notification event = %#v", notificationEvent)
	}

	filter := db.ListDeploymentsWithRefsFilteredParams{
		FProjectID: sql.NullInt64{Int64: fixture.projectID, Valid: true},
		FEnvID:     sql.NullInt64{Int64: fixture.environmentID, Valid: true},
		FStatus:    sql.NullString{String: "pending", Valid: true},
		FFromUnix: sql.NullInt64{
			Int64: fixture.deployment.CreatedAt,
			Valid: true,
		},
		FToUnix: sql.NullInt64{
			Int64: fixture.deployment.CreatedAt,
			Valid: true,
		},
		PageOffset: 0,
		PageLimit:  1,
	}
	deployments, err := fixture.queries.ListDeploymentsWithRefsFiltered(
		fixture.ctx,
		filter,
	)
	requireNoError(t, err, "list filtered deployments")
	if len(deployments) != 1 || deployments[0].ID != fixture.deployment.ID {
		t.Fatalf("filtered deployments = %#v", deployments)
	}
	count, err := fixture.queries.CountDeploymentsWithRefsFiltered(
		fixture.ctx,
		db.CountDeploymentsWithRefsFilteredParams{
			FProjectID: filter.FProjectID,
			FEnvID:     filter.FEnvID,
			FStatus:    filter.FStatus,
			FFromUnix:  filter.FFromUnix,
			FToUnix:    filter.FToUnix,
		},
	)
	requireNoError(t, err, "count filtered deployments")
	if count != 1 {
		t.Fatalf("filtered deployment count = %d, want 1", count)
	}

	approval, err := fixture.queries.CreateApproval(
		fixture.ctx,
		db.CreateApprovalParams{
			DeploymentID: fixture.deployment.ID,
			ApprovedBy:   "mssql-user@example.com",
			ApproverUserID: sql.NullInt64{
				Int64: fixture.userID,
				Valid: true,
			},
			RequiredApproverRole: "admin",
		},
	)
	requireNoError(t, err, "create deployment approval")
	if approval.ID == 0 || approval.ApprovedAt <= 0 {
		t.Fatalf("created deployment approval = %#v", approval)
	}
	persistedApproval, err := fixture.queries.GetApprovalByDeployment(
		fixture.ctx,
		fixture.deployment.ID,
	)
	requireNoError(t, err, "get deployment approval")
	if persistedApproval != approval {
		t.Fatalf(
			"persisted deployment approval = %#v, want %#v",
			persistedApproval,
			approval,
		)
	}
}
