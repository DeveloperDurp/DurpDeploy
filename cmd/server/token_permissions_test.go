package main

import (
	"context"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/roles"
	"durpdeploy/internal/testutil"
)

func TestRunTokensCreatePermissionLevels(t *testing.T) {
	dsn := tempDSN(t)
	t.Setenv("DURPDEPLOY_DB", dsn)
	const email = "cli-roles@example.test"
	createTestAdmin(t, email, "fixture-password-123")
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, conn) })
	q := db.New(conn)
	project, err := q.CreateProject(
		context.Background(),
		db.CreateProjectParams{Name: "CLI project"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"global", "scoped"} {
		for _, role := range roles.All() {
			t.Run(scope+"/"+role, func(t *testing.T) {
				args := []string{
					"create",
					"--user",
					email,
					"--name",
					scope + role,
					"--scope",
					scope,
					"--role",
					role,
				}
				if scope == "scoped" {
					args = append(args, "--project-id", fmt.Sprint(project.ID))
				}
				captureStdout(t, func() {
					if code := runTokens(args); code != 0 {
						t.Fatalf("exit=%d", code)
					}
				})
				rows, err := q.ListApiTokensByUser(t.Context(), 1)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, row := range rows {
					if row.Name != scope+role {
						continue
					}
					found = true
					if row.Scope != scope || row.TokenRole != role {
						t.Fatal("CLI lost permission ceiling")
					}
					ids, err := q.ListApiTokenProjectIDs(t.Context(), row.ID)
					if err != nil {
						t.Fatal(err)
					}
					if scope == "scoped" &&
						(len(ids) != 1 || ids[0] != project.ID) {
						t.Fatal("CLI lost project selection")
					}
				}
				if !found {
					t.Fatal("CLI did not persist token")
				}
			})
		}
	}
	for _, grant := range []repository.TokenScope{
		{Scope: "scoped", Role: "maintainer"},
		{Scope: "global", Role: "owner"},
	} {
		if code := runTokens([]string{"create", "--user", email, "--name", "invalid",
			"--scope", grant.Scope, "--role", grant.Role}); code == 0 {
			t.Fatal("invalid grant accepted")
		}
	}
}
