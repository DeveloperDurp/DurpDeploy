package handler

import (
	"net/http"
	"path/filepath"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/testutil/sqlitefixture"
)

// setupTestRepo uses real migrations and waits for SQLite connections to
// close before TempDir removes their files.
func setupTestRepo(t *testing.T) *repository.Repository {
	t.Helper()
	sqlDB := sqlitefixture.Open(t, filepath.Join(t.TempDir(), "test.db"),
		"_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	return repository.New(sqlDB)
}

func streamTestAuth(
	t *testing.T,
	repo *repository.Repository,
) func(http.Handler) http.Handler {
	t.Helper()
	user, err := repo.Queries.CreateUser(
		t.Context(),
		db.CreateUserParams{
			Email:        "stream@example.com",
			Name:         "Stream",
			PasswordHash: "unused",
			Role:         "admin",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	token, _, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateApiToken(
		t.Context(),
		db.CreateApiTokenParams{
			ID:        "stream-token",
			UserID:    user.ID,
			TokenHash: hash,
			Name:      "Stream",
			Scope:     "global",
		},
	); err != nil {
		t.Fatal(err)
	}
	return func(next http.Handler) http.Handler {
		authenticated := auth.ApiTokenMiddleware(repo)(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+token)
			authenticated.ServeHTTP(w, r)
		})
	}
}

func seedStreamTestDeployment(t *testing.T, repo *repository.Repository) {
	t.Helper()
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "stream"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "stream"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        "running",
		},
	); err != nil {
		t.Fatal(err)
	}
}
