package auth_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

// newAccessTestRepo boots an in-memory SQLite, runs all migrations, and
// returns a Repository wrapping it. Schema comes from migrate.Run — no
// inline SQL (see AGENTS.md warning about logs_test.go).
func newAccessTestRepo(t *testing.T) *repository.Repository {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return repository.New(conn)
}

// seedUser creates a user with the given role and returns its id.
func seedUser(
	t *testing.T,
	repo *repository.Repository,
	email, role string,
) *db.User {
	t.Helper()
	u, err := repo.Queries.CreateUser(context.Background(), db.CreateUserParams{
		Email:        email,
		PasswordHash: "fakehash",
		Name:         email,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return &u
}

// seedProject creates a project and returns its id.
func seedProject(t *testing.T, repo *repository.Repository, name string) int64 {
	t.Helper()
	p, err := repo.Queries.CreateProject(
		context.Background(),
		db.CreateProjectParams{
			Name:        name,
			Description: sql.NullString{Valid: false},
		},
	)
	if err != nil {
		t.Fatalf("create project %q: %v", name, err)
	}
	return p.ID
}

// addMember adds a project_members row.
func addMember(
	t *testing.T,
	repo *repository.Repository,
	projectID, userID int64,
	role string,
) {
	t.Helper()
	if err := repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: projectID,
			UserID:    userID,
			Role:      role,
		},
	); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

// runMiddleware mounts RequireProjectAccess on a chi route with the
// given id, injects the user into context, and returns the response
// status code. The inner handler returns 200 so a pass-through is
// observable as http.StatusOK.
func runMiddleware(
	t *testing.T,
	repo *repository.Repository,
	user *db.User,
	projectID string,
) int {
	t.Helper()
	r := chi.NewRouter()
	r.With(
		auth.RequireProjectAccess(repo),
	).Get("/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID, nil)
	req = auth.SetUser(req, user)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func TestRequireProjectAccess(t *testing.T) {
	cases := []struct {
		name      string
		userRole  string
		addMember bool
		projectID string
		want      int
	}{
		{
			name:      "global admin bypasses membership check",
			userRole:  "admin",
			addMember: false,
			projectID: "1",
			want:      http.StatusOK,
		},
		{
			name:      "member can access",
			userRole:  "deployer",
			addMember: true,
			projectID: "1",
			want:      http.StatusOK,
		},
		{
			name:      "non-member gets 403",
			userRole:  "deployer",
			addMember: false,
			projectID: "1",
			want:      http.StatusForbidden,
		},
		{
			name:      "non-existent project gets 404",
			userRole:  "deployer",
			addMember: false,
			projectID: "9999",
			want:      http.StatusNotFound,
		},
		{
			name:      "invalid project_id gets 400",
			userRole:  "deployer",
			addMember: false,
			projectID: "abc",
			want:      http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newAccessTestRepo(t)
			user := seedUser(t, repo, "u@example.com", tc.userRole)
			pid := seedProject(t, repo, "test-project")
			if tc.addMember {
				addMember(t, repo, pid, user.ID, "deployer")
			}

			// For the non-existent-project case, use the literal id
			// from tc.projectID (no project seeded with that id).
			got := runMiddleware(t, repo, user, tc.projectID)
			if got != tc.want {
				t.Fatalf("status: got %d want %d", got, tc.want)
			}
		})
	}
}

func TestRequireProjectAccess_NoUser(t *testing.T) {
	repo := newAccessTestRepo(t)
	seedProject(t, repo, "test-project")

	r := chi.NewRouter()
	r.With(
		auth.RequireProjectAccess(repo),
	).Get("/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/projects/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestProjectIDFromContext(t *testing.T) {
	repo := newAccessTestRepo(t)
	user := seedUser(t, repo, "m@example.com", "deployer")
	pid := seedProject(t, repo, "member-project")
	addMember(t, repo, pid, user.ID, "deployer")

	var ctxID int64
	r := chi.NewRouter()
	r.With(
		auth.RequireProjectAccess(repo),
	).Get("/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.ProjectIDFromContext(r.Context())
		if !ok {
			t.Fatal("expected project id in context")
		}
		ctxID = id
		w.WriteHeader(http.StatusOK)
	})

	req := auth.SetUser(
		httptest.NewRequest(http.MethodGet, "/projects/1", nil),
		user,
	)
	r.ServeHTTP(httptest.NewRecorder(), req)
	if ctxID != pid {
		t.Fatalf("context project id: got %d want %d", ctxID, pid)
	}
}
