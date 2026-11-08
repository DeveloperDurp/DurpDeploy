package static

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerRejectsDirectories(t *testing.T) {
	h := Handler()
	for _, target := range []string{
		"/static/", "/static/css", "/static/css/", "/static/js",
		"/static/icons/", "/static/swagger-ui", "/static/swagger-ui/",
		"/static/css/..", "/static/css/../swagger-ui",
		"/static//css//", "/static/%63ss", "/static/css?v=old-build",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+target, func(t *testing.T) {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, target, nil))
				if w.Code != http.StatusNotFound {
					t.Fatalf("status=%d, want 404", w.Code)
				}
				if w.Header().Get("Location") != "" {
					t.Fatal("directory request redirected")
				}
			})
		}
	}
}

func TestHandlerServesEmbeddedFiles(t *testing.T) {
	h := Handler()
	err := fs.WalkDir(
		Assets,
		".",
		func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			t.Run(name, func(t *testing.T) {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", URL(name), nil))
				content, err := Assets.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK ||
					w.Body.String() != string(content) {
					t.Fatalf(
						"asset %s: status=%d or body mismatch",
						name,
						w.Code,
					)
				}
			})
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}
