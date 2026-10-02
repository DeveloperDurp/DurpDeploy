package handler_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestWeb_RequestBodyPreservesMultipartFields(t *testing.T) {
	h := newAuthHarness(t)
	session := seedSession(t, h.repo, h.server, "admin")
	for _, headerToken := range []bool{false, true} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		name := "multipart-form-token"
		if headerToken {
			name = "multipart-header-token"
		} else if err := form.WriteField("csrf_token", session.csrfToken); err != nil {
			t.Fatal(err)
		}
		if err := form.WriteField("name", name); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", h.server+"/environments", &body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", form.FormDataContentType())
		if headerToken {
			req.Header.Set("X-CSRF-Token", session.csrfToken)
		}
		resp, err := session.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 303 {
			t.Fatalf("header=%t status=%d", headerToken, resp.StatusCode)
		}
		var stored string
		if err := h.repo.DB.QueryRow("SELECT name FROM environments WHERE name = ?", name).
			Scan(&stored); err != nil ||
			stored != name {
			t.Fatalf("multipart fields lost: name=%q err=%v", stored, err)
		}
	}
}

func TestWeb_RequestBodyPreservesViewerRejection(t *testing.T) {
	h := newAuthHarness(t)
	session := seedSession(t, h.repo, h.server, "viewer")
	for _, body := range []string{"name=%zz", strings.Repeat("x", 16<<20+1)} {
		for _, htmx := range []bool{false, true} {
			req, err := http.NewRequest(
				"POST",
				h.server+"/projects",
				strings.NewReader(body),
			)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if htmx {
				req.Header.Set("HX-Request", "true")
			}
			resp, err := session.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if htmx {
				if resp.StatusCode != 200 ||
					!strings.Contains(
						resp.Header.Get("HX-Trigger"),
						"makeToast",
					) {
					t.Fatalf(
						"viewer toast: status=%d trigger=%q",
						resp.StatusCode,
						resp.Header.Get("HX-Trigger"),
					)
				}
			} else if resp.StatusCode != 403 || !bytes.Contains(data, []byte("Viewers cannot perform write operations")) {
				t.Fatalf("viewer page: status=%d body=%s", resp.StatusCode, data)
			}
		}
	}
}

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
