package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestAudit_SetAction_records_parsed_manual_override(t *testing.T) {
	// Given
	ctx := context.Background()
	connection, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	repo := repository.New(connection)
	user, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email: "override@example.com", PasswordHash: "hash", Name: "Override",
		Role: "deployer",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	router := chi.NewRouter()
	router.Use(Middleware(repo))
	router.Post("/projects/{id}/deploy", http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		SetAction(r, "create_deployment_override", "deployment")
		w.WriteHeader(http.StatusNoContent)
	}))
	request := auth.SetUser(
		httptest.NewRequest(http.MethodPost, "/projects/1/deploy", nil),
		&user,
	)

	// When
	router.ServeHTTP(httptest.NewRecorder(), request)

	// Then
	entries, err := repo.Queries.ListAuditLogs(ctx, 1)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Action != "create_deployment_override" ||
		entries[0].EntityType != "deployment" ||
		!entries[0].EntityID.Valid || entries[0].EntityID.Int64 != 1 {
		t.Fatalf("audit entry = %#v, want deployment override", entries[0])
	}
}
