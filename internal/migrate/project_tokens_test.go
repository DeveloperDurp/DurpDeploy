package migrate

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/testutil"
	"durpdeploy/migrations"
	"github.com/pressly/goose/v3"
)

func TestProjectTokenQueriesSQLite(t *testing.T) {
	conn, err := Run(t.TempDir() + "/tokens.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, conn) })
	testProjectTokenQueries(t, conn)
}

func TestSQLServer_ProjectTokenQueries(t *testing.T) {
	testProjectTokenQueries(t, newSQLServerTestDB(t))
}

func TestProjectTokenUpgradeAndRollbackRefusal(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	requireNoError(t, err, "open legacy database")
	t.Cleanup(func() { testutil.Close(t, conn) })
	goose.SetBaseFS(migrations.FS)
	requireNoError(t, goose.SetDialect("sqlite3"), "set dialect")
	requireNoError(t, goose.UpTo(conn, ".", 62), "migrate legacy schema")
	_, err = conn.ExecContext(t.Context(), `
		INSERT INTO users(id,email,password_hash,name,role)
		VALUES(1,'legacy-token@example.test','hash','Legacy','admin');
		INSERT INTO api_tokens(id,user_id,name,token_prefix,token_hash,scope)
		VALUES('legacy-global',1,'Legacy','ddp_pat_','hash','global')`)
	requireNoError(t, err, "seed existing global token")
	requireNoError(t, goose.UpTo(conn, ".", 63), "upgrade token schema")
	row, err := db.New(conn).GetApiTokenByID(t.Context(), "legacy-global")
	requireNoError(t, err, "read migrated global token")
	if row.Scope != "global" || row.TokenRole != "" {
		t.Fatal("migration changed existing global permissions")
	}
	if err := goose.Down(conn, "."); err == nil {
		t.Fatal("unsafe token migration rollback succeeded")
	}
	version, err := goose.GetDBVersion(conn)
	requireNoError(t, err, "read version after refused rollback")
	if version != 63 {
		t.Fatal("refused rollback changed schema version")
	}
	_, err = db.New(conn).ListApiTokenProjectIDs(t.Context(), row.ID)
	requireNoError(t, err, "grant table survives refused rollback")
}

func testProjectTokenQueries(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := t.Context()
	q := db.New(conn)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "project-token-parity@example.test", Name: "Token parity",
		PasswordHash: "hash", Role: "deployer",
	})
	requireNoError(t, err, "create token owner")
	project, err := q.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "token scope parity"},
	)
	requireNoError(t, err, "create selected project")
	err = q.AddProjectMember(ctx, db.AddProjectMemberParams{
		ProjectID: project.ID, UserID: user.ID, Role: "admin",
	})
	requireNoError(t, err, "add membership")
	full, prefix, hash, err := auth.MintApiToken()
	requireNoError(t, err, "mint backend token")
	token, err := q.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID:          "scope-parity-token",
		UserID:      user.ID,
		Name:        "parity",
		TokenPrefix: prefix,
		TokenHash:   hash,
		Scope:       "global",
	})
	requireNoError(t, err, "create legacy token")
	if token.Scope != "global" || token.TokenRole != "" {
		t.Fatal("legacy token default changed")
	}
	_, err = q.SetApiTokenScope(ctx, db.SetApiTokenScopeParams{
		ID: token.ID, Scope: "scoped", TokenRole: "owner",
	})
	if err == nil {
		t.Fatal("invalid token role accepted")
	}
	_, err = q.SetApiTokenScope(ctx, db.SetApiTokenScopeParams{
		ID: token.ID, Scope: "scoped", TokenRole: "viewer",
	})
	requireNoError(t, err, "set viewer ceiling")
	err = q.AddApiTokenProject(ctx, db.AddApiTokenProjectParams{
		TokenID: token.ID, ProjectID: project.ID,
	})
	requireNoError(t, err, "add selected project")
	err = q.AddApiTokenProject(ctx, db.AddApiTokenProjectParams{
		TokenID: token.ID, ProjectID: project.ID,
	})
	if err == nil {
		t.Fatal("duplicate token project accepted")
	}
	rows, err := q.ListTokenProjectsPaginated(
		ctx,
		db.ListTokenProjectsPaginatedParams{
			TokenID: token.ID, UserID: user.ID, PageLimit: 10,
		},
	)
	requireNoError(t, err, "list selected projects")
	if len(rows) != 1 || rows[0].ID != project.ID {
		t.Fatal("selected project missing")
	}
	_, err = q.ListTokenEnvironments(ctx, db.ListTokenEnvironmentsParams{
		TokenID: token.ID, UserID: user.ID,
	})
	requireNoError(t, err, "query eligible environments")
	_, err = q.ListTokenLifecycles(ctx, db.ListTokenLifecyclesParams{
		TokenID: token.ID, UserID: user.ID,
	})
	requireNoError(t, err, "query assigned lifecycles")
	_, err = q.DeploymentActivity(ctx, db.DeploymentActivityParams{
		TokenID: token.ID, UserID: user.ID, FromUnix: 0, ToUnix: 1,
	})
	requireNoError(t, err, "query scoped activity")
	repo := repository.New(conn)
	previous, err := repo.ReplaceTokenScope(ctx, token.ID,
		repository.TokenScope{Scope: "scoped", Role: "viewer",
			ProjectIDs: []int64{project.ID}})
	requireNoError(t, err, "replace grants and capture prior scope")
	if previous.Role != "viewer" || len(previous.ProjectIDs) != 1 ||
		previous.ProjectIDs[0] != project.ID {
		t.Fatal("backend scope edit lost previous grants")
	}
	middleware := auth.ApiTokenMiddleware(repo)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	request.Header.Set("Authorization", "Bearer "+full)
	response := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.RoleFromContext(r.Context()) != "viewer" ||
			!auth.TokenAllowsProject(r.Context(), project.ID) {
			t.Error("backend token authentication lost its grants")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	middleware(next).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("backend token authentication: %d %s", response.Code,
			response.Body.String())
	}
	err = q.DeleteProject(ctx, project.ID)
	requireNoError(t, err, "delete selected project")
	ids, err := q.ListApiTokenProjectIDs(ctx, token.ID)
	requireNoError(t, err, "read grants after project deletion")
	if len(ids) != 0 {
		t.Fatal("deleted project grant retained")
	}
	response = httptest.NewRecorder()
	middleware(next).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatal("empty backend grants did not fail closed")
	}
	_, err = repo.ReplaceTokenScope(ctx, token.ID,
		repository.TokenScope{Scope: "global", Role: "viewer"})
	requireNoError(t, err, "set all-project viewer ceiling")
	response = httptest.NewRecorder()
	middleware(next).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("all-project backend token authentication: %d %s",
			response.Code, response.Body.String())
	}
}
