package migrate

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestSQLServer_SourceQueryRewritesPersistResults(t *testing.T) {
	ctx := context.Background()
	dbConn := newSQLServerTestDB(t)
	queries := db.New(dbConn)

	project, err := queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "rewrite-project"},
	)
	requireNoError(t, err, "create project")
	environment, err := queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "rewrite-environment"},
	)
	requireNoError(t, err, "create environment")
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "rewrite-v1", StepsJson: "[]",
	})
	requireNoError(t, err, "create release")
	_, err = queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	requireNoError(t, err, "create deployment")

	notification, err := queries.CreateNotificationEvent(
		ctx,
		db.CreateNotificationEventParams{
			EventType:     "deployment_started",
			ProjectID:     sql.NullInt64{Int64: project.ID, Valid: true},
			EnvironmentID: sql.NullInt64{Int64: environment.ID, Valid: true},
			Message:       "rewrite notification",
			Results:       "{}",
		},
	)
	requireNoError(t, err, "create notification event")

	notifications, err := queries.ListNotificationEvents(ctx, 1)
	requireNoError(t, err, "list notification events with TOP rewrite")
	if len(notifications) != 1 || notifications[0].ID != notification.ID ||
		notifications[0].ProjectName.String != project.Name ||
		notifications[0].EnvironmentName.String != environment.Name {
		t.Fatalf("listed notification events = %#v", notifications)
	}

	count, err := queries.CountDeploymentsToday(ctx)
	requireNoError(t, err, "count deployments with start-of-day rewrite")
	if count != 1 {
		t.Fatalf("deployments today = %d, want 1", count)
	}

	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "rewrite-user@example.com", PasswordHash: "hash", Name: "Rewrite", Role: "viewer",
	})
	requireNoError(t, err, "create user")
	token, err := queries.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID: "rewrite-token", UserID: user.ID, Name: "rewrite", TokenPrefix: "ddp_pat_",
		TokenHash: "rewrite-token-hash", Scope: "global",
	})
	requireNoError(t, err, "create token")
	requireNoError(
		t,
		queries.RevokeApiToken(ctx, token.ID),
		"revoke token with strftime rewrite",
	)
	var revokedAt sql.NullInt64
	err = dbConn.QueryRowContext(ctx,
		"SELECT revoked_at FROM api_tokens WHERE id = @p1", token.ID,
	).Scan(&revokedAt)
	requireNoError(t, err, "get revoked token timestamp")
	if !revokedAt.Valid || revokedAt.Int64 <= 0 {
		t.Fatalf("revoked token timestamp = %#v", revokedAt)
	}
}
