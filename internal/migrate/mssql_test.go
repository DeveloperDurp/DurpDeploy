package migrate

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestSQLServer_MigrationsAndQueries(t *testing.T) {
	ctx := context.Background()
	dbConn := newSQLServerTestDB(t)
	queries := db.New(dbConn)
	project, err := queries.CreateProject(ctx, db.CreateProjectParams{
		Name:        "mssql-project",
		Description: sql.NullString{String: "created", Valid: true},
	})
	requireNoError(t, err, "create project")
	if project.ID == 0 || project.Name != "mssql-project" ||
		!project.Description.Valid || project.Description.String != "created" ||
		project.CreatedAt <= 0 || project.LifecycleID.Valid {
		t.Fatalf("created project = %#v", project)
	}

	updatedProject, err := queries.UpdateProject(ctx, db.UpdateProjectParams{
		ID: project.ID, Name: "mssql-project-updated",
	})
	requireNoError(t, err, "update project")
	if updatedProject.ID != project.ID ||
		updatedProject.Name != "mssql-project-updated" ||
		updatedProject.Description.Valid {
		t.Fatalf("updated project = %#v", updatedProject)
	}
	persistedProject, err := queries.GetProject(ctx, project.ID)
	requireNoError(t, err, "get updated project")
	if persistedProject != updatedProject {
		t.Fatalf(
			"persisted project = %#v, want %#v",
			persistedProject,
			updatedProject,
		)
	}
	lifecycle, err := queries.CreateLifecycle(
		ctx, db.CreateLifecycleParams{Name: "mssql-lifecycle"},
	)
	requireNoError(t, err, "create lifecycle")
	err = queries.SetProjectLifecycle(ctx, db.SetProjectLifecycleParams{
		ID: project.ID,
		LifecycleID: sql.NullInt64{
			Int64: lifecycle.ID,
			Valid: true,
		},
	})
	requireNoError(t, err, "set project lifecycle")
	err = queries.DeleteLifecycle(ctx, lifecycle.ID)
	requireNoError(t, err, "delete lifecycle")
	persistedProject, err = queries.GetProject(ctx, project.ID)
	requireNoError(t, err, "get project after lifecycle deletion")
	if persistedProject.LifecycleID.Valid {
		t.Fatalf(
			"project lifecycle after deletion = %#v, want NULL",
			persistedProject.LifecycleID,
		)
	}

	verifyMSSQLProjectPagination(t, ctx, queries)

	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "mssql-user@example.com",
		PasswordHash: "hash",
		Name:         "MSSQL User",
		Role:         "deployer",
	})
	requireNoError(t, err, "create user")
	if user.CreatedAt <= 0 || user.UpdatedAt <= 0 || user.LastLoginAt.Valid {
		t.Fatalf("created user = %#v", user)
	}
	const loginAt = int64(1_700_000_000)
	err = queries.UpdateUserLastLogin(ctx, db.UpdateUserLastLoginParams{
		ID: user.ID, LastLoginAt: sql.NullInt64{Int64: loginAt, Valid: true},
	})
	requireNoError(t, err, "update user login timestamp")
	persistedUser, err := queries.GetUserByID(ctx, user.ID)
	requireNoError(t, err, "get updated user")
	if !persistedUser.LastLoginAt.Valid ||
		persistedUser.LastLoginAt.Int64 != loginAt {
		t.Fatalf(
			"persisted user login timestamp = %#v",
			persistedUser.LastLoginAt,
		)
	}

	member := db.AddProjectMemberParams{
		ProjectID: project.ID, UserID: user.ID, Role: "admin",
	}
	err = queries.AddProjectMember(ctx, member)
	requireNoError(t, err, "add project member")
	member.Role = "deployer"
	err = queries.AddProjectMember(ctx, member)
	requireNoError(t, err, "upsert project member")
	membership := db.IsProjectMemberParams{
		ProjectID: project.ID,
		UserID:    user.ID,
	}
	isMember, err := queries.IsProjectMember(ctx, membership)
	requireNoError(t, err, "check present project member")
	if isMember != 1 {
		t.Fatalf("present project member = %d, want 1", isMember)
	}
	membership.UserID++
	isMember, err = queries.IsProjectMember(ctx, membership)
	requireNoError(t, err, "check absent project member")
	if isMember != 0 {
		t.Fatalf("absent project member = %d, want 0", isMember)
	}
	persistedMember, err := queries.GetProjectMember(
		ctx,
		db.GetProjectMemberParams{
			ProjectID: project.ID, UserID: user.ID,
		},
	)
	requireNoError(t, err, "get project member")
	memberCount, err := queries.CountProjectMembers(ctx, project.ID)
	requireNoError(t, err, "count project members")
	if persistedMember.Role != "deployer" || persistedMember.CreatedAt <= 0 ||
		memberCount != 1 {
		t.Fatalf(
			"persisted member = %#v, count = %d",
			persistedMember,
			memberCount,
		)
	}

	global, err := queries.UpdateGlobalNotifications(
		ctx,
		db.UpdateGlobalNotificationsParams{
			SlackWebhookUrl: sql.NullString{
				String: "https://hooks.example.test/slack",
				Valid:  true,
			},
			GotifyToken: sql.NullString{String: "gotify-token", Valid: true},
		},
	)
	requireNoError(t, err, "update global notifications")
	if global.ID != 1 || global.UpdatedAt <= 0 ||
		!global.SlackWebhookUrl.Valid || global.SlackWebhookUrl.String != "https://hooks.example.test/slack" ||
		global.NotifyEmails.Valid || global.GotifyUrl.Valid ||
		!global.GotifyToken.Valid || global.GotifyToken.String != "gotify-token" ||
		global.DiscordWebhookUrl.Valid {
		t.Fatalf("updated global notifications = %#v", global)
	}
	persistedGlobal, err := queries.GetGlobalNotifications(ctx)
	requireNoError(t, err, "get global notifications")
	if persistedGlobal != global {
		t.Fatalf(
			"persisted global notifications = %#v, want %#v",
			persistedGlobal,
			global,
		)
	}

	environment, err := queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "mssql-env"},
	)
	requireNoError(t, err, "create environment")
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	requireNoError(t, err, "create release")
	deployment, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	requireNoError(t, err, "create deployment")
	if deployment.ID == 0 || deployment.StartedAt.Valid ||
		deployment.FinishedAt.Valid ||
		deployment.Note.Valid ||
		deployment.CreatedAt <= 0 {
		t.Fatalf("created deployment = %#v", deployment)
	}
	persistedDeployment, err := queries.GetDeployment(ctx, deployment.ID)
	requireNoError(t, err, "get deployment")
	if persistedDeployment != deployment {
		t.Fatalf(
			"persisted deployment = %#v, want %#v",
			persistedDeployment,
			deployment,
		)
	}
	verifyMSSQLDeploymentQueries(t, mssqlDeploymentFixture{
		ctx:           ctx,
		queries:       queries,
		projectID:     project.ID,
		environmentID: environment.ID,
		deployment:    deployment,
		userID:        user.ID,
	})

	tx, err := dbConn.BeginTx(ctx, nil)
	requireNoError(t, err, "begin committed transaction")
	committedProject, err := queries.WithTx(tx).CreateProject(
		ctx,
		db.CreateProjectParams{Name: "mssql-committed"},
	)
	requireNoError(t, err, "create committed project")
	requireNoError(t, tx.Commit(), "commit transaction")
	_, err = queries.GetProject(ctx, committedProject.ID)
	requireNoError(t, err, "get committed project")

	tx, err = dbConn.BeginTx(ctx, nil)
	requireNoError(t, err, "begin rolled-back transaction")
	rolledBackProject, err := queries.WithTx(tx).CreateProject(
		ctx,
		db.CreateProjectParams{Name: "mssql-rolled-back"},
	)
	requireNoError(t, err, "create rolled-back project")
	requireNoError(t, tx.Rollback(), "rollback transaction")
	if _, err := queries.GetProject(
		ctx,
		rolledBackProject.ID,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("get rolled-back project error = %v, want sql.ErrNoRows", err)
	}
}

func requireNoError(t *testing.T, err error, operation string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
}
