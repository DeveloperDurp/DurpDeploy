package server

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/handler"
)

// Bound web requests after authentication (or public MFA rate limiting) and
// before CSRF can parse them, even with a header token.
func webRequestBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete:
		default:
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/") ||
			r.URL.Path == "/api/v1" || r.URL.Path == "/api/lint" ||
			r.URL.Path == "/login" ||
			r.URL.Path == "/settings/security/reauth" {
			next.ServeHTTP(w, r)
			return
		}
		if auth.RejectViewerWrite(w, r) {
			return
		}
		limit := handler.MaxFormBodyBytes
		if strings.Contains(r.URL.Path, "/package-repository") {
			limit = 64 << 10
		}
		body, err := handler.ReadRequestBody(w, r, limit)
		closeErr := r.Body.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "Request body too large", 413)
			} else {
				http.Error(w, "Invalid request body", 400)
			}
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		// Keep MaxBytesReader around the restored body so ParseForm uses
		// the deliberate form ceiling instead of its implicit 10 MiB cap.
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType == "multipart/form-data" {
			err = r.ParseMultipartForm(limit)
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
		} else {
			err = r.ParseForm()
		}
		if err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		next.ServeHTTP(w, r)
	})
}
