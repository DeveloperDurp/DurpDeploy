package server

import (
	"bytes"
	"io"
	"net/http"
)

// ponytail: 64 KiB leaves room for legacy credentials while keeping queued
// password forms negligible beside the two active 64 MiB Argon2 hashes.
const maxPasswordFormBodyBytes int64 = 64 << 10

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
		body, err := io.ReadAll(
			io.LimitReader(r.Body, maxPasswordFormBodyBytes+1),
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
		if int64(len(body)) > maxPasswordFormBodyBytes {
			http.Error(w, "request body too large", status)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
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
