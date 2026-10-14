package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestAuthorizationToastKeepsPageAndHistory(t *testing.T) {
	for _, test := range []struct {
		name   string
		reject func(http.ResponseWriter, *http.Request)
	}{
		{"viewer write", func(w http.ResponseWriter, r *http.Request) {
			auth.RejectViewerWrite(w, r)
		}},
		{"project access", auth.RenderUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/projects/1/deploy", nil)
			r.Header.Set("HX-Request", "true")
			r = auth.SetUser(r, &db.User{Role: "viewer"})
			w := httptest.NewRecorder()
			test.reject(w, r)
			if w.Code != http.StatusOK || w.Body.Len() != 0 ||
				w.Header().Get("HX-Reswap") != "none" ||
				w.Header().Get("HX-Push-Url") != "false" {
				t.Fatalf("toast response replaces page or history: %+v", w)
			}
			var events map[string]struct{ Level, Message string }
			if err := json.Unmarshal([]byte(w.Header().Get("HX-Trigger")),
				&events); err != nil || events["makeToast"].Level != "danger" ||
				events["makeToast"].Message == "" {
				t.Fatalf(
					"missing authorization error toast: %+v %v",
					events,
					err,
				)
			}
		})
	}
}
