package handler_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/handler"
)

func TestReadJSONContentType(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "PATCH"} {
		for _, tc := range []struct {
			media string
			valid bool
		}{
			{"application/json", true},
			{"application/json; charset=utf-8", true},
			{`Application/JSON; profile="example"`, true},
			{"", false},
			{"text/plain", false},
			{"application/x-www-form-urlencoded", false},
			{"application/json; charset", false},
		} {
			t.Run(method+"/"+tc.media, func(t *testing.T) {
				req := httptest.NewRequest(method, "/api/v1/projects",
					strings.NewReader(`{"name":"example"}`))
				req.Header.Set("Content-Type", tc.media)
				rec := httptest.NewRecorder()
				var body struct{ Name string }
				if handler.ReadJSON(rec, req, &body) != tc.valid {
					t.Fatalf("response=%d %s", rec.Code, rec.Body)
				}
				if !tc.valid && (rec.Code != 415 || body.Name != "") {
					t.Fatalf(
						"invalid media type decoded: %d %+v",
						rec.Code,
						body,
					)
				}
			})
		}
	}
}

func TestReadJSONContentTypeLegacyPaths(t *testing.T) {
	// Browser handlers and empty-body control actions keep their own contracts.
	req := httptest.NewRequest("POST", "/api/lint",
		strings.NewReader(`{"script":"true"}`))
	var body struct{ Script string }
	if !handler.ReadJSON(httptest.NewRecorder(), req, &body) {
		t.Fatal("browser JSON parser changed")
	}
	for _, payload := range []string{"", "{}"} {
		req := httptest.NewRequest("POST", "/api/v1/control",
			strings.NewReader(payload))
		if !handler.ReadEmptyJSON(httptest.NewRecorder(), req) {
			t.Fatal("empty control action now requires a media type")
		}
	}
}
