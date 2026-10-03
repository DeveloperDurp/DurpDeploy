package api

import (
	"net/http"

	"durpdeploy/internal/handler"
)

// RequestBodyLimit covers every API route, including ignored request bodies.
func RequestBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxJSONBodyBytes)
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			!handler.ReadEmptyJSON(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// EmptyBody validates control actions before their handler can mutate state.
func EmptyBody(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if handler.ReadEmptyJSON(w, r) {
			next(w, r)
		}
	}
}
