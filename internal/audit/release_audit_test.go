package audit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestReleaseDeleteAuditUsesReleaseID(t *testing.T) {
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	for _, path := range []string{
		"/projects/{id}/releases/{releaseId}",
		"/api/v1/projects/{id}/releases/{relId}",
	} {
		t.Run(path, func(t *testing.T) {
			router := chi.NewRouter()
			router.Use(Middleware(repo))
			router.Delete(path, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			url := "/projects/11/releases/42"
			if strings.HasPrefix(path, "/api/") {
				url = "/api/v1" + url
			}
			router.ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodDelete, url, nil))
			var entityType string
			var entityID int64
			if err := conn.QueryRow(`SELECT entity_type, entity_id
FROM audit_log WHERE action = 'delete_release' ORDER BY id DESC LIMIT 1`).
				Scan(&entityType, &entityID); err != nil {
				t.Fatal(err)
			}
			if entityType != "release" || entityID != 42 {
				t.Fatalf("audit entity=%s/%d", entityType, entityID)
			}
		})
	}
}
