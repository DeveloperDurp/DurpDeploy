package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/config"
)

// Limits count encoded bytes, including script escaping and form overhead.
const (
	MaxJSONBodyBytes         = config.DefaultJSONBodyBytes
	MaxFormBodyBytes         = config.DefaultFormBodyBytes
	MaxPasswordFormBodyBytes = config.DefaultPasswordFormBodyBytes
)

type requestBodyLimitsKey struct{}

// WithRequestBodyLimits captures the router's validated immutable policy.
func WithRequestBodyLimits(
	limits config.HTTPLimits,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(
				r.Context(),
				requestBodyLimitsKey{},
				limits,
			)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestBodyLimits preserves defaults for standalone handler callers.
func RequestBodyLimits(r *http.Request) config.HTTPLimits {
	if limits, ok := r.Context().Value(requestBodyLimitsKey{}).(config.HTTPLimits); ok {
		return limits
	}
	return config.Defaults().Limits.HTTP
}

var errInvalidJSONBody = errors.New("invalid JSON body")

// ReadRequestBody checks the entire body before any mutation or decoding.
// Reading through MaxBytesReader also bounds unknown-length requests.
func ReadRequestBody(
	w http.ResponseWriter,
	r *http.Request,
	limit int64,
) ([]byte, error) {
	if r.ContentLength > limit {
		// Preserve the original body's early-close state for net/http.
		return nil, &http.MaxBytesError{Limit: limit}
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return body, nil
}

// WriteRequestBodyError hides parser details behind stable JSON errors.
func WriteRequestBodyError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		auth.RenderJSONError(w, 413, "Request body too large")
		return
	}
	auth.RenderJSONError(w, 400, "Invalid JSON body")
}

func decodeJSONBody(body []byte, v any) error {
	// All public JSON request schemas are objects; null must not silently
	// leave a zero-value struct that can enter a mutation path.
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errInvalidJSONBody
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errInvalidJSONBody
	}
	return nil
}

// ReadJSON requires one bounded, closed-schema JSON object.
func ReadJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := ReadRequestBody(w, r, RequestBodyLimits(r).JSONBytes)
	if err == nil {
		err = decodeJSONBody(body, v)
	}
	if err != nil {
		WriteRequestBodyError(w, err)
		return false
	}
	return true
}

// ReadEmptyJSON allows legacy empty-body actions and explicit {} payloads.
func ReadEmptyJSON(w http.ResponseWriter, r *http.Request) bool {
	body, err := ReadRequestBody(w, r, RequestBodyLimits(r).JSONBytes)
	if err == nil && len(body) != 0 {
		err = decodeJSONBody(body, &struct{}{})
	}
	if err != nil {
		WriteRequestBodyError(w, err)
		return false
	}
	return true
}
