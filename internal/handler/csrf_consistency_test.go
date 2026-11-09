package handler_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCSRFTokenComparisonAndPrecedence(t *testing.T) {
	h := newProjectHarness(t)
	token := h.csrfToken()
	for _, tc := range []struct {
		name, form, header string
		status             int
	}{
		{"header", "", token, 303},
		{"form wins", token, "wrong", 303},
		{"invalid form wins", "wrong", token, 403},
		{"missing", "", "", 403},
		{"short", "", token[:len(token)-1], 403},
		{"long", "", token + "x", 403},
		{"same length wrong", "", strings.Repeat("!", len(token)), 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{
				"name":       {"csrf-" + tc.name},
				"csrf_token": {tc.form},
			}
			req, err := http.NewRequestWithContext(
				t.Context(),
				"POST",
				h.server.URL+"/projects",
				strings.NewReader(form.Encode()),
			)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-CSRF-Token", tc.header)
			resp, err := h.authedClient().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d", resp.StatusCode, tc.status)
			}
		})
	}
}
