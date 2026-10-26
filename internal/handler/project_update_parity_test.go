package handler_test

import (
	"database/sql"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/testutil"
)

func TestProjectUpdateLifecyclePermissionRace(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			conn, err := migrate.Run(projectUpdateDSN(t, backend))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { testutil.Close(t, conn) })
			repo := repository.New(conn)
			project, err := repo.Queries.CreateProject(t.Context(),
				db.CreateProjectParams{Name: "permission-race"})
			if err != nil {
				t.Fatal(err)
			}
			lifecycle, err := repo.Queries.CreateLifecycle(t.Context(),
				db.CreateLifecycleParams{Name: "permission-race"})
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.Queries.SetProjectLifecycle(t.Context(),
				db.SetProjectLifecycleParams{ID: project.ID,
					LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true}},
			); err != nil {
				t.Fatal(err)
			}
			user, err := repo.Queries.CreateUser(
				t.Context(),
				db.CreateUserParams{
					Email: "permission-race@example.test", Name: "maintainer",
					Role: "maintainer", PasswordHash: "unused fixture hash",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			member := db.AddProjectMemberParams{
				ProjectID: project.ID, UserID: user.ID, Role: "admin",
			}
			if err := repo.Queries.AddProjectMember(t.Context(), member); err != nil {
				t.Fatal(err)
			}
			ctx := auth.SetUser(httptest.NewRequest("PUT", "/", nil), &user).
				Context()
			if err := handler.AuthorizeLifecycleSelection(
				ctx, repo, project.ID, 0,
			); err != nil {
				t.Fatal(err)
			}
			// Commit revocation after preflight but before the update transaction.
			member.Role = "maintainer"
			if err := repo.Queries.AddProjectMember(ctx, member); err != nil {
				t.Fatal(err)
			}
			_, err = handler.UpdateProjectWithLifecycle(
				ctx,
				repo,
				db.UpdateProjectParams{
					ID:   project.ID,
					Name: "must roll back",
				},
				0,
			)
			if !errors.Is(err, handler.ErrLifecycleRemovalForbidden) {
				t.Fatalf("revoked permission was not rechecked: %v", err)
			}
			actual, err := repo.Queries.GetProject(ctx, project.ID)
			if err != nil || actual.Name != project.Name ||
				!actual.LifecycleID.Valid || actual.LifecycleID.Int64 != lifecycle.ID {
				t.Fatalf(
					"revoked removal changed project: %+v, %v",
					actual,
					err,
				)
			}
			current, err := repo.Queries.GetProjectMember(
				ctx,
				db.GetProjectMemberParams{
					ProjectID: project.ID,
					UserID:    user.ID,
				},
			)
			if err != nil || current.Role != "maintainer" {
				t.Fatalf("concurrent revocation lost: %+v, %v", current, err)
			}
		})
	}
}

func projectUpdateDSN(t *testing.T, backend string) string {
	t.Helper()
	switch backend {
	case "SQLite":
		return "file:" + filepath.Join(t.TempDir(), "project.db") +
			"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)" +
			"&_pragma=busy_timeout(5000)"
	case "PostgreSQL":
		container, err := postgres.Run(
			t.Context(),
			"harbor.durp.info/dockerhub/library/postgres:16-alpine",
			postgres.WithDatabase("project"),
			postgres.WithUsername("project"),
			postgres.WithPassword(
				"fixture-only",
			),
			postgres.BasicWaitStrategies(),
		)
		testutil.CleanupContainer(t, container)
		if err != nil {
			t.Fatal(err)
		}
		dsn, err := container.ConnectionString(t.Context(), "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
		return dsn
	case "SQLServer":
		return testutil.SQLServerDSN(t)
	default:
		t.Fatalf("unknown backend %q", backend)
		return ""
	}
}
