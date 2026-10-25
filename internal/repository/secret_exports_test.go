package repository_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestSecretExportDatabaseParity(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			testSecretExportBackend(t, backend)
		})
	}
}

func testSecretExportBackend(t *testing.T, backend string) {
	t.Helper()
	var repo *repository.Repository
	if backend == "SQLite" {
		repo = newTestRepo(t)
	} else {
		conn, err := migrate.Run(repository.RepositoryDatabaseDSN(t, backend))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
		})
		repo = repository.New(conn)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	user, err := repo.Queries.CreateUser(
		t.Context(),
		db.CreateUserParams{
			Email:        "export-parity@example.test",
			Name:         "Export parity",
			Role:         "admin",
			PasswordHash: "unused-fixture-hash",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Queries.CreateSession(
		t.Context(),
		db.CreateSessionParams{
			ID:        "export-parity-session",
			UserID:    user.ID,
			CsrfToken: "fixture-csrf",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "Export parity"},
	)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "Export shared parity"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateVariable(
		t.Context(),
		db.CreateVariableParams{
			ProjectID: project.ID,
			Name:      "PASSWORD",
			Secret:    1,
			Value: sql.NullString{
				String: "harmless-parity-secret",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.SaveLifecycleVariable(
		t.Context(),
		repository.LifecycleVariableInput{
			CreateLifecycleVariableParams: db.CreateLifecycleVariableParams{
				LifecycleID: lifecycle.ID,
				Name:        "PASSWORD",
				Secret:      1,
				Value: sql.NullString{
					String: "harmless-parity-secret",
					Valid:  true,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"project", "lifecycle"} {
		t.Run(scope, func(t *testing.T) {
			id := project.ID
			if scope == "lifecycle" {
				id = lifecycle.ID
			}
			testSecretExportScope(t, repo, user.ID, scope, id)
		})
	}
}

func testSecretExportScope(
	t *testing.T,
	repo *repository.Repository,
	userID int64,
	scope string,
	id int64,
) {
	t.Helper()
	row, claim, err := repo.NewSecretExport(
		t.Context(),
		db.CreateSecretExportParams{
			UserID:  userID,
			TokenID: "fixture-token",
			Scope:   scope,
			ScopeID: id,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	count, err := repo.Queries.BindSecretExport(
		t.Context(),
		db.BindSecretExportParams{
			ID:        row.ID,
			UserID:    userID,
			SessionID: "export-parity-session",
			ExpiresAt: time.Now().Unix(),
		},
	)
	if err != nil || count != 1 {
		t.Fatalf("bind = %d, %v", count, err)
	}
	count, err = repo.Queries.SetSecretExportAuth(
		t.Context(),
		db.SetSecretExportAuthParams{
			ID:        row.ID,
			UserID:    userID,
			SessionID: "export-parity-session",
			AuthHash: repository.SecretExportHash(
				"fixture-mfa",
			),
			ExpiresAt: time.Now().Unix(),
		},
	)
	if err != nil || count != 1 {
		t.Fatalf("MFA binding = %d, %v", count, err)
	}
	bound, err := repo.Queries.SecretExportForAuth(
		t.Context(),
		db.SecretExportForAuthParams{
			UserID:    userID,
			SessionID: "export-parity-session",
			AuthHash: repository.SecretExportHash(
				"fixture-mfa",
			),
			ExpiresAt: time.Now().Unix(),
		},
	)
	if err != nil || bound != row.ID {
		t.Fatalf("MFA lookup = %s, %v", bound, err)
	}
	now := time.Now().Unix()
	count, err = repo.Queries.ApproveSecretExport(
		t.Context(),
		db.ApproveSecretExportParams{
			ID:        row.ID,
			UserID:    userID,
			SessionID: "export-parity-session",
			ExpiresAt: now,
			Now:       now,
		},
	)
	if err != nil || count != 1 {
		t.Fatalf("approve = %d, %v", count, err)
	}
	if _, err := repo.TakeSecretExport(t.Context(), row.ID, userID, "", "fixture-token", "wrong-claim"); !errors.Is(
		err,
		repository.ErrSecretExportInvalid,
	) {
		t.Fatalf("wrong claim = %v", err)
	}
	doc, err := repo.TakeSecretExport(
		t.Context(),
		row.ID,
		userID,
		"",
		"fixture-token",
		claim,
	)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Scope != scope || doc.ScopeID != id ||
		len(doc.Variables) != 1 ||
		doc.Variables[0].Value != "harmless-parity-secret" {
		t.Fatal("incorrect export content")
	}
	if _, err := repo.TakeSecretExport(t.Context(), row.ID, userID, "", "fixture-token", claim); !errors.Is(
		err,
		repository.ErrSecretExportInvalid,
	) {
		t.Fatalf("replay = %v", err)
	}
}
