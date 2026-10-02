package handler_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWeb_RequestBodyRejectsOversizedFormsBeforeMutation(t *testing.T) {
	// Given: a real browser session and production HTTP server.
	h := newAuthHarness(t)
	session := seedSession(t, h.repo, h.server, "admin")
	for _, contentType := range []string{
		"application/x-www-form-urlencoded",
		"multipart/form-data; boundary=boundary",
		"text/plain",
	} {
		for _, chunked := range []bool{false, true} {
			body := "name=rejected&padding=" + strings.Repeat("a", 16<<20)
			req, err := http.NewRequest(
				"POST",
				h.server+"/projects",
				strings.NewReader(body),
			)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-CSRF-Token", session.csrfToken)
			if chunked {
				req.ContentLength = -1
			}
			// When
			resp, err := session.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.Copy(io.Discard, resp.Body)
			closeErr := resp.Body.Close()
			// Then
			if readErr != nil || closeErr != nil || resp.StatusCode != 413 {
				t.Fatalf("type=%s chunked=%t status=%d read=%v close=%v",
					contentType, chunked, resp.StatusCode, readErr, closeErr)
			}
		}
	}
	var count int
	if err := h.repo.DB.QueryRow("SELECT count(*) FROM projects").Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("oversized form mutated projects: %d err=%v", count, err)
	}
}

func TestWeb_LintRequestBodyStrictness(t *testing.T) {
	// Given: lint is session protected but available to a viewer.
	h := newAuthHarness(t)
	session := seedSession(t, h.repo, h.server, "viewer")
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"script":"echo hi","extra":true}`, 400},
		{`{"script":"echo hi"} {}`, 400},
		{`null`, 400},
		{`{"script":"` + strings.Repeat("a", 4<<20) + `"}`, 413},
		{`{"script":"echo hi"}` + " \n", 200},
	} {
		req, err := http.NewRequest(
			"POST",
			h.server+"/api/lint",
			strings.NewReader(tc.body),
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = -1
		// When
		resp, err := session.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		// Then
		if readErr != nil || closeErr != nil || resp.StatusCode != tc.status {
			t.Fatalf(
				"status=%d want=%d read=%v close=%v",
				resp.StatusCode,
				tc.status,
				readErr,
				closeErr,
			)
		}
	}
}
