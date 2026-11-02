package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/handler"
)

const (
	maxLintBodyBytes     = 524_288
	maxLintResponseBytes = 32_768
	lintAnalysisTimeout  = 2 * time.Second
	lintRequestStopped   = "lint request stopped"
)

var errLintRequest = errors.New("invalid lint request")

type lintRequest struct {
	Profile string
	Script  string
}

type lintResponse struct {
	Profile     string                   `json:"profile"`
	Diagnostics []handler.LintDiagnostic `json:"diagnostics"`
	Truncated   bool                     `json:"truncated"`
}

type LintHandler struct {
	// ponytail: four router-owned slots; revisit capacity after measurement.
	slots chan struct{}
}

func NewLintHandler() *LintHandler {
	return &LintHandler{slots: make(chan struct{}, 4)}
}

// LintNoStore also covers failures before bearer authentication completes.
func LintNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.IsReadOnlyLintRequest(r) {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (h *LintHandler) Analyze(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Context().Err() != nil {
		auth.RenderJSONError(
			w,
			http.StatusRequestTimeout,
			lintRequestStopped,
		)
		return
	}
	if !validLintContentType(r.Header.Get("Content-Type")) {
		auth.RenderJSONError(w, http.StatusUnsupportedMediaType,
			"lint requires UTF-8 application/json")
		return
	}
	if r.URL.RawQuery != "" {
		auth.RenderJSONError(w, http.StatusBadRequest, "invalid lint request")
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		if r.ProtoMajor == 1 {
			// Do not drain an unfinished HTTP/1 body before the busy reply.
			w.Header().Set("Connection", "close")
		}
		auth.RenderJSONError(w, http.StatusServiceUnavailable, "lint is busy")
		return
	}
	body, err := handler.ReadRequestBody(w, r,
		min(int64(maxLintBodyBytes), handler.RequestBodyLimits(r).JSONBytes))
	if err != nil {
		writeLintBodyError(w, r, err)
		return
	}
	req, err := decodeLintRequest(r.Context(), body)
	if err != nil {
		writeLintDecodeError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lintAnalysisTimeout)
	defer cancel()
	diagnostics, truncated, err := handler.LintBearerWarnings(ctx, req.Script)
	if err != nil {
		writeLintAnalysisError(w, r, err)
		return
	}
	encoded, err := json.Marshal(lintResponse{
		Profile:     handler.BearerLintProfile,
		Diagnostics: diagnostics, Truncated: truncated,
	})
	if err != nil || len(encoded) > maxLintResponseBytes {
		auth.RenderJSONError(
			w,
			http.StatusInternalServerError,
			"lint response failed",
		)
		return
	}
	if err := ctx.Err(); err != nil {
		writeLintAnalysisError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(encoded)))
	w.WriteHeader(http.StatusOK)
	// A failed delivery cannot be replaced by another complete response.
	_, _ = w.Write(encoded)
}

func validLintContentType(value string) bool {
	media, params, err := mime.ParseMediaType(value)
	charset, hasCharset := params["charset"]
	return err == nil && media == "application/json" &&
		(!hasCharset || strings.EqualFold(charset, "utf-8"))
}

func writeLintBodyError(w http.ResponseWriter, r *http.Request, err error) {
	var timeout net.Error
	if r.Context().Err() != nil ||
		errors.As(err, &timeout) && timeout.Timeout() {
		auth.RenderJSONError(w, http.StatusRequestTimeout, lintRequestStopped)
		return
	}
	handler.WriteRequestBodyError(w, err)
}

func writeLintDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
		auth.RenderJSONError(w, http.StatusRequestTimeout, lintRequestStopped)
	case errors.Is(err, handler.ErrBearerLintInput):
		auth.RenderJSONError(w, http.StatusRequestEntityTooLarge,
			"lint script is too large")
	default:
		auth.RenderJSONError(w, http.StatusBadRequest, "invalid lint request")
	}
}

func writeLintAnalysisError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case r.Context().Err() != nil:
		auth.RenderJSONError(
			w,
			http.StatusRequestTimeout,
			lintRequestStopped,
		)
	case errors.Is(err, context.DeadlineExceeded):
		auth.RenderJSONError(
			w,
			http.StatusGatewayTimeout,
			"lint analysis timed out",
		)
	default:
		auth.RenderJSONError(
			w,
			http.StatusInternalServerError,
			"lint analysis failed",
		)
	}
}

func decodeLintRequest(ctx context.Context, body []byte) (lintRequest, error) {
	var req lintRequest
	if !utf8.Valid(body) || !json.Valid(body) || !validLintUnicode(ctx, body) {
		return req, errLintRequest
	}
	d := json.NewDecoder(bytes.NewReader(body))
	req, err := decodeLintFields(d)
	if err != nil {
		return req, err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return req, errLintRequest
	}
	if len(req.Script) > handler.MaxBearerLintScriptBytes {
		return req, handler.ErrBearerLintInput
	}
	return req, ctx.Err()
}

func decodeLintFields(d *json.Decoder) (lintRequest, error) {
	var req lintRequest
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return req, errLintRequest
	}
	seen := uint8(0)
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return req, errLintRequest
		}
		value, err := d.Token()
		s, ok := value.(string)
		if err != nil || !ok || strings.ContainsRune(s, 0) {
			return req, errLintRequest
		}
		if err := assignLintField(&req, key, s, &seen); err != nil {
			return req, err
		}
	}
	if _, err := d.Token(); err != nil || seen != 3 ||
		req.Profile != handler.BearerLintProfile {
		return req, errLintRequest
	}
	return req, nil
}

func assignLintField(
	req *lintRequest,
	key any,
	value string,
	seen *uint8,
) error {
	switch key {
	case "profile":
		if *seen&1 != 0 {
			return errLintRequest
		}
		*seen |= 1
		req.Profile = value
	case "script":
		if *seen&2 != 0 {
			return errLintRequest
		}
		*seen |= 2
		req.Script = value
	default:
		return errLintRequest
	}
	return nil
}

// JSON decoding replaces lone surrogates. Reject them before decoding instead.
// json.Valid has already checked escape syntax and bounds for this scan.
func validLintUnicode(ctx context.Context, body []byte) bool {
	for i := 0; i < len(body); i++ {
		if i%4096 == 0 && ctx.Err() != nil {
			return false
		}
		if body[i] != '\\' {
			continue
		}
		i++
		if body[i] != 'u' {
			continue
		}
		var valid bool
		i, valid = lintUnicodeEscape(body, i)
		if !valid {
			return false
		}
	}
	return ctx.Err() == nil
}

// i points to a syntactically valid JSON Unicode escape's 'u'.
func lintUnicodeEscape(body []byte, i int) (int, bool) {
	code, _ := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
	i += 4
	if code >= 0xdc00 && code <= 0xdfff {
		return i, false
	}
	if code < 0xd800 || code > 0xdbff {
		return i, true
	}
	if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
		return i, false
	}
	low, _ := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
	return i + 6, low >= 0xdc00 && low <= 0xdfff
}
