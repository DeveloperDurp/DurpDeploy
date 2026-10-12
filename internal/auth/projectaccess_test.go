package auth_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestDeploymentAccessErrorFormats(t *testing.T) {
	for _, prefix := range []string{"/api/v1", ""} {
		for _, test := range []struct {
			name, id string
			loggedIn bool
			status   int
		}{
			{"unauthenticated", "1", false, http.StatusUnauthorized},
			{"malformed id", "invalid", true, http.StatusBadRequest},
			{"missing deployment", "1", true, http.StatusNotFound},
		} {
			t.Run(prefix+"/"+test.name, func(t *testing.T) {
				// Given: an API or web request rejected before its handler.
				repo := newAccessTestRepo(t)
				router := chi.NewRouter()
				router.With(auth.RequireDeploymentProjectAccess(repo)).Get(
					prefix+"/deployments/{id}",
					func(http.ResponseWriter, *http.Request) {
						t.Error("rejected request reached the handler")
					})
				req := httptest.NewRequest(http.MethodGet,
					prefix+"/deployments/"+test.id, nil)
				if test.loggedIn {
					req = auth.SetUser(req,
						seedUser(t, repo, "access@example.com", "admin"))
				}
				rec := httptest.NewRecorder()

				// When: deployment access is checked.
				router.ServeHTTP(rec, req)

				// Then: API errors are JSON; web errors remain plain text.
				if rec.Code != test.status {
					t.Fatalf("status=%d, want=%d", rec.Code, test.status)
				}
				if prefix != "" {
					var envelope map[string]string
					if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope["error"] == "" || !strings.HasPrefix(
						rec.Header().Get("Content-Type"), "application/json") {
						t.Fatal("missing JSON error response")
					}
				} else if !strings.HasPrefix(rec.Header().Get("Content-Type"),
					"text/plain") || rec.Body.Len() == 0 {
					t.Fatal("web error response changed")
				}
			})
		}
	}
}

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
