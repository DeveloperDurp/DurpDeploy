package migrate

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestSQLServer_SchemaParityDefaultsAndIndexes(t *testing.T) {
	ctx := context.Background()
	dbConn := newSQLServerTestDB(t)
	queries := db.New(dbConn)

	project, err := queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "parity-project"},
	)
	requireNoError(t, err, "create project")
	environment, err := queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "parity-environment"},
	)
	requireNoError(t, err, "create environment")
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "parity-v1",
		StepsJson: "[]",
	})
	requireNoError(t, err, "create release")
	deployment, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	requireNoError(t, err, "create deployment")

	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO deployment_approvals (deployment_id, approved_by)
		VALUES (@p1, @p2)`, deployment.ID, "default@example.com")
	requireNoError(t, err, "create approval with defaults")
	approval, err := queries.GetApprovalByDeployment(ctx, deployment.ID)
	requireNoError(t, err, "get approval with defaults")
	if approval.ApprovedAt <= 0 || approval.RequiredApproverRole != "admin" {
		t.Fatalf("approval defaults = %#v", approval)
	}

	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO notification_events (event_type, message)
		VALUES (@p1, @p2)`, "parity", "default results")
	requireNoError(t, err, "create notification event with default results")
	var results string
	var createdAt int64
	err = dbConn.QueryRowContext(ctx, `
		SELECT TOP (1) results, created_at FROM notification_events
		ORDER BY id DESC`).Scan(&results, &createdAt)
	requireNoError(t, err, "get notification event with defaults")
	if results != "{}" || createdAt <= 0 {
		t.Fatalf(
			"notification defaults = results %q, created at %d",
			results,
			createdAt,
		)
	}

	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "parity-user@example.com", PasswordHash: "hash", Name: "Parity", Role: "viewer",
	})
	requireNoError(t, err, "create user")
	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, name, role)
		VALUES (@p1, @p2, @p3, @p4)`,
		"parity-admin@example.com", "hash", "Parity Admin", "admin")
	requireNoError(t, err, "create user with valid role")
	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, name, role)
		VALUES (@p1, @p2, @p3, @p4)`,
		"parity-invalid@example.com", "hash", "Parity Invalid", "owner")
	if err == nil {
		t.Fatal("create user with invalid role succeeded")
	}
	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES (@p1, @p2, @p3)`, project.ID, user.ID, "admin")
	requireNoError(t, err, "create project member with valid role")
	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES (@p1, @p2, @p3)`, project.ID, user.ID, "viewer")
	if err == nil {
		t.Fatal("create project member with invalid role succeeded")
	}
	_, err = dbConn.ExecContext(
		ctx,
		`
		INSERT INTO api_tokens (id, user_id, name, token_prefix, token_hash)
		VALUES (@p1, @p2, @p3, @p4, @p5)`,
		"parity-default-token",
		user.ID,
		"default",
		"ddp_pat_",
		"default-token-hash",
	)
	requireNoError(t, err, "create token with default scope")
	var defaultScope string
	err = dbConn.QueryRowContext(ctx,
		"SELECT scope FROM api_tokens WHERE id = @p1", "parity-default-token",
	).Scan(&defaultScope)
	requireNoError(t, err, "get token default scope")
	if defaultScope != "global" {
		t.Fatalf("token default scope = %q, want global", defaultScope)
	}
	_, err = dbConn.ExecContext(
		ctx,
		`
		INSERT INTO api_tokens (id, user_id, name, token_prefix, token_hash, scope)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6)`,
		"parity-scoped-token",
		user.ID,
		"scoped",
		"ddp_pat_",
		"scoped-token-hash",
		"scoped",
	)
	requireNoError(t, err, "create token with valid scope")
	_, err = dbConn.ExecContext(
		ctx,
		`
		INSERT INTO api_tokens (id, user_id, name, token_prefix, token_hash, scope)
		VALUES (@p1, @p2, @p3, @p4, @p5, @p6)`,
		"parity-invalid-token",
		user.ID,
		"invalid",
		"ddp_pat_",
		"invalid-token-hash",
		"project",
	)
	if err == nil {
		t.Fatal("create token with invalid scope succeeded")
	}
	_, err = queries.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID: "parity-token-1", UserID: user.ID, Name: "first", TokenPrefix: "ddp_pat_",
		TokenHash: "parity-token-hash", Scope: "global", ExpiresAt: sql.NullInt64{},
	})
	requireNoError(t, err, "create first token")
	_, err = queries.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID: "parity-token-2", UserID: user.ID, Name: "duplicate", TokenPrefix: "ddp_pat_",
		TokenHash: "parity-token-hash", Scope: "global", ExpiresAt: sql.NullInt64{},
	})
	if err == nil {
		t.Fatal("create duplicate token hash succeeded")
	}
	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO deployment_approvals (deployment_id, approved_by, approver_user_id)
		VALUES (@p1, @p2, @p3)`, deployment.ID, "approver@example.com", user.ID)
	requireNoError(t, err, "create approval with approver")
	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO audit_log (user_id, action, entity_type)
		VALUES (@p1, @p2, @p3)`, user.ID, "parity", "user")
	requireNoError(t, err, "create audit log with user")
	_, err = dbConn.ExecContext(ctx, `
		INSERT INTO notification_events (event_type, environment_id, message)
		VALUES (@p1, @p2, @p3)`, "parity", environment.ID, "environment foreign key")
	requireNoError(t, err, "create notification event with environment")
	_, err = dbConn.ExecContext(
		ctx,
		"DELETE FROM users WHERE id = @p1",
		user.ID,
	)
	requireNoError(t, err, "delete user")
	for _, lookup := range []struct {
		query string
		arg   string
	}{
		{"SELECT approver_user_id FROM deployment_approvals WHERE approved_by = @p1", "approver@example.com"},
		{"SELECT user_id FROM audit_log WHERE action = @p1", "parity"},
	} {
		var value sql.NullInt64
		err := dbConn.QueryRowContext(ctx, lookup.query, lookup.arg).
			Scan(&value)
		requireNoError(t, err, "read set-null foreign key")
		if value.Valid {
			t.Fatalf("set-null foreign key = %#v, want NULL", value)
		}
	}
	_, err = dbConn.ExecContext(
		ctx,
		"DELETE FROM environments WHERE id = @p1",
		environment.ID,
	)
	requireNoError(t, err, "delete environment")
	var environmentID sql.NullInt64
	err = dbConn.QueryRowContext(ctx, `
		SELECT environment_id FROM notification_events WHERE message = @p1`,
		"environment foreign key",
	).Scan(&environmentID)
	requireNoError(t, err, "read notification environment foreign key")
	if environmentID.Valid {
		t.Fatalf(
			"notification environment foreign key = %#v, want NULL",
			environmentID,
		)
	}

	for _, index := range []struct{ table, name string }{
		{"project_members", "idx_project_members_user_id"},
		{"sessions", "idx_sessions_user_id"},
		{"sessions", "idx_sessions_expires_at"},
		{"releases", "idx_releases_project_id"},
		{"deployments", "idx_deployments_release_id"},
		{"deployments", "idx_deployments_environment_id"},
		{"notification_events", "idx_notification_events_created_at"},
		{"notification_events", "idx_notification_events_project_id"},
		{"audit_log", "idx_audit_log_user_id"},
		{"audit_log", "idx_audit_log_created_at"},
		{"audit_log", "idx_audit_log_action"},
		{"api_tokens", "idx_api_tokens_user_id"},
		{"api_tokens", "idx_api_tokens_revoked_at"},
		{"deployment_approvals", "idx_deployment_approvals_deployment_id"},
	} {
		var count int
		err := dbConn.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sys.indexes
			WHERE object_id = OBJECT_ID(@p1) AND name = @p2`,
			"dbo."+index.table,
			index.name,
		).Scan(&count)
		requireNoError(t, err, "look up "+index.name)
		if count != 1 {
			t.Fatalf(
				"index %s on %s count = %d, want 1",
				index.name,
				index.table,
				count,
			)
		}
	}
}
