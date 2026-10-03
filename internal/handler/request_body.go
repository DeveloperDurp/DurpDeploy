package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"durpdeploy/internal/auth"
)

// Limits count encoded bytes, including script escaping and form overhead.
const (
	MaxJSONBodyBytes int64 = 4 << 20
	MaxFormBodyBytes int64 = 16 << 20
)

var errInvalidJSONBody = errors.New("Invalid JSON body")

// ReadRequestBody checks the entire body before any mutation or decoding.
// Reading through MaxBytesReader also bounds unknown-length requests.
func ReadRequestBody(
	w http.ResponseWriter,
	r *http.Request,
	limit int64,
) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if r.ContentLength > limit {
		return nil, &http.MaxBytesError{Limit: limit}
	}
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
	body, err := ReadRequestBody(w, r, MaxJSONBodyBytes)
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
	body, err := ReadRequestBody(w, r, MaxJSONBodyBytes)
	if err == nil && len(body) != 0 {
		err = decodeJSONBody(body, &struct{}{})
	}
	if err != nil {
		WriteRequestBodyError(w, err)
		return false
	}
	return true
}
