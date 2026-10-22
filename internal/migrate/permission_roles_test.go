package migrate

import (
	"database/sql"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/roles"
	"durpdeploy/internal/testutil"
	"github.com/pressly/goose/v3"
)

func TestPermissionRolesUpgrade(t *testing.T) {
	forEachRemoteClaimDatabase(t, testPermissionRolesUpgrade)
}

func testPermissionRolesUpgrade(t *testing.T, fixture remoteClaimTestDB) {
	fixture.baselineVersion = 63
	if fixture.gooseDialect == "mssql" {
		fixture.baselineVersion = 48
	}
	conn := fixture.openBaseline(t)
	t.Cleanup(func() { testutil.Close(t, conn) })
	q := db.New(conn)
	ctx := t.Context()
	// Seed the historical schema without current generated project columns.
	var project db.Project
	err := conn.QueryRowContext(
		ctx,
		"INSERT INTO projects(name) VALUES(?) RETURNING id",
		"role upgrade",
	).Scan(&project.ID)
	requireNoError(t, err, "create project")
	var users []db.User
	for _, role := range []string{"viewer", "deployer", "admin"} {
		users = append(
			users,
			seedPermissionRoleIdentity(t, conn, q, project.ID, role),
		)
	}
	rolesVersion := fixture.baselineVersion + 1
	requireNoError(
		t,
		goose.UpTo(conn, ".", rolesVersion),
		"upgrade roles",
	)
	requireNoError(
		t,
		goose.UpTo(conn, ".", rolesVersion),
		"repeat startup",
	)
	for _, before := range users {
		assertPermissionRoleIdentity(t, q, project.ID, before)
	}
	for _, table := range []string{"oidc_identities", "mfa_totp", "sessions"} {
		var count int
		err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).
			Scan(&count)
		requireNoError(t, err, "count retained references")
		if count != 3 {
			t.Fatalf("%s references lost: %d", table, count)
		}
	}
	for _, role := range roles.All() {
		user, err := q.CreateUser(
			ctx,
			db.CreateUserParams{Email: role + "@new.test",
				Name: role, PasswordHash: "hash", Role: role},
		)
		requireNoError(t, err, "create four-level user")
		requireNoError(
			t,
			q.AddProjectMember(ctx, db.AddProjectMemberParams{
				ProjectID: project.ID, UserID: user.ID, Role: role}),
			"create four-level membership",
		)
		_, err = q.SetApiTokenScope(ctx, db.SetApiTokenScopeParams{
			ID: "admin-global", Scope: "global", TokenRole: role})
		requireNoError(t, err, "set four-level token ceiling")
	}
	for _, query := range []string{
		"UPDATE users SET role='owner'", "UPDATE project_members SET role='owner'",
		"UPDATE api_tokens SET token_role='owner'",
	} {
		if _, err := conn.ExecContext(ctx, query); err == nil {
			t.Fatal("invalid role accepted")
		}
	}
	version, err := goose.GetDBVersion(conn)
	requireNoError(t, err, "read version")
	if err := goose.Down(conn, "."); err == nil {
		t.Fatal("unsafe rollback succeeded")
	}
	after, err := goose.GetDBVersion(conn)
	requireNoError(t, err, "read version after refusal")
	if after != version {
		t.Fatal("rollback refusal changed version")
	}
	t.Logf(
		"%s: preserved identities, credentials, grants, references, and rollback guard at %d",
		fixture.name,
		version,
	)
}

func TestPermissionRolesFreshSQLiteConstraints(t *testing.T) {
	conn, err := Run(":memory:")
	requireNoError(t, err, "fresh migration")
	t.Cleanup(func() { testutil.Close(t, conn) })
	for _, role := range roles.All() {
		_, err := db.New(conn).CreateUser(t.Context(), db.CreateUserParams{
			Email: fmt.Sprintf("%s@fresh.test", role), Name: role,
			PasswordHash: "hash", Role: role,
		})
		requireNoError(t, err, "create role")
	}
}

func seedPermissionRoleIdentity(
	t *testing.T,
	conn *sql.DB,
	q *db.Queries,
	projectID int64,
	role string,
) db.User {
	ctx := t.Context()
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: role + "@upgrade.test", PasswordHash: "retained hash",
		Name: role, Role: role,
	})
	requireNoError(t, err, "seed legacy user")
	requireNoError(
		t,
		q.AddProjectMember(ctx, db.AddProjectMemberParams{
			ProjectID: projectID, UserID: user.ID, Role: "deployer",
		}),
		"seed legacy membership",
	)
	_, err = q.CreateSession(ctx, db.CreateSessionParams{
		ID: role, UserID: user.ID, CsrfToken: "retained csrf", ExpiresAt: 4102444800,
	})
	requireNoError(t, err, "seed session")
	_, err = q.CreateOIDCIdentity(ctx, db.CreateOIDCIdentityParams{
		Issuer: "https://upgrade.test", Subject: role, UserID: user.ID,
	})
	requireNoError(t, err, "seed OIDC identity")
	_, err = conn.ExecContext(
		ctx,
		"INSERT INTO mfa_totp(user_id,encrypted_seed) VALUES(?,?)",
		user.ID,
		[]byte("retained seed"),
	)
	requireNoError(t, err, "seed MFA factor")
	for _, scope := range []string{"global", "scoped"} {
		id := role + "-" + scope
		_, err = q.CreateApiToken(ctx, db.CreateApiTokenParams{
			ID: id, UserID: user.ID, Name: id, TokenPrefix: "ddp_pat_1234",
			TokenHash: id, Scope: scope,
		})
		requireNoError(t, err, "seed token")
		if scope == "scoped" {
			_, err = q.SetApiTokenScope(
				ctx,
				db.SetApiTokenScopeParams{
					ID: id, Scope: scope, TokenRole: "deployer",
				},
			)
			requireNoError(t, err, "seed token ceiling")
			requireNoError(t, q.AddApiTokenProject(
				ctx,
				db.AddApiTokenProjectParams{
					TokenID:   id,
					ProjectID: projectID,
				},
			), "seed token project")
		}
	}
	return user
}

func assertPermissionRoleIdentity(
	t *testing.T,
	q *db.Queries,
	projectID int64,
	before db.User,
) {
	ctx := t.Context()
	want := before
	if want.Role == "deployer" {
		want.Role = "maintainer"
	}
	after, err := q.GetUserByID(ctx, before.ID)
	requireNoError(t, err, "read upgraded user")
	if after != want {
		t.Fatalf("user changed: %+v, want %+v", after, want)
	}
	member, err := q.GetProjectMember(
		ctx,
		db.GetProjectMemberParams{
			ProjectID: projectID,
			UserID:    before.ID,
		},
	)
	requireNoError(t, err, "read upgraded membership")
	if member.Role != "maintainer" {
		t.Fatal("authoring membership was not preserved")
	}
	session, err := q.GetSession(
		ctx,
		db.GetSessionParams{ID: before.Name, ExpiresAt: 0},
	)
	requireNoError(t, err, "read retained session")
	if session.Role != want.Role ||
		session.CsrfToken != "retained csrf" {
		t.Fatal("session changed")
	}
	for _, scope := range []string{"global", "scoped"} {
		id := before.Name + "-" + scope
		token, err := q.GetApiTokenByID(ctx, id)
		requireNoError(t, err, "read retained token")
		wantRole := ""
		if scope == "scoped" {
			wantRole = "maintainer"
		}
		if token.Scope != scope || token.TokenRole != wantRole ||
			token.TokenHash != id {
			t.Fatal(
				"token scope, ceiling, or hash changed incorrectly",
			)
		}
	}
	ids, err := q.ListApiTokenProjectIDs(ctx, before.Name+"-scoped")
	requireNoError(t, err, "read retained token project")
	if len(ids) != 1 || ids[0] != projectID {
		t.Fatal("token project was lost")
	}
}
