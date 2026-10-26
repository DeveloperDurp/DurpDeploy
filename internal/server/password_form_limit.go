package server

import (
	"bytes"
	"io"
	"net/http"

	"durpdeploy/internal/handler"
)

func passwordFormBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		status := http.StatusBadRequest
		parseErrorMessage := ""
		switch r.URL.Path {
		case "/login":
		case "/settings/security/reauth":
			status = http.StatusUnprocessableEntity
			parseErrorMessage = "Unable to reauthenticate"
		default:
			next.ServeHTTP(w, r)
			return
		}
		limit := handler.RequestBodyLimits(r).PasswordFormBytes
		body, err := io.ReadAll(
			io.LimitReader(r.Body, limit+1),
		)
		if err != nil {
			if parseErrorMessage == "" {
				parseErrorMessage = err.Error()
			}
			http.Error(w, parseErrorMessage, status)
			return
		}
		if err := r.Body.Close(); err != nil {
			if parseErrorMessage == "" {
				parseErrorMessage = err.Error()
			}
			http.Error(w, parseErrorMessage, status)
			return
		}
		if int64(len(body)) > limit {
			http.Error(w, "request body too large", status)
			return
		}
		r.Body = http.MaxBytesReader(
			w,
			io.NopCloser(bytes.NewReader(body)),
			limit,
		)
		r.ContentLength = int64(len(body))
		if err := r.ParseForm(); err != nil {
			if parseErrorMessage == "" {
				parseErrorMessage = err.Error()
			}
			http.Error(w, parseErrorMessage, status)
			return
		}
		next.ServeHTTP(w, r)
	})
}
