package server

import "net/http"

// ponytail: 64 KiB leaves room for legacy credentials while keeping queued
// password forms negligible beside the two active 64 MiB Argon2 hashes.
const maxPasswordFormBodyBytes int64 = 64 << 10

func passwordFormBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			switch r.URL.Path {
			case "/login", "/settings/security/reauth":
				r.Body = http.MaxBytesReader(
					w,
					r.Body,
					maxPasswordFormBodyBytes,
				)
			}
		}
		next.ServeHTTP(w, r)
	})
}
