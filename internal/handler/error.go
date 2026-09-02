package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5/middleware"

	"durpdeploy/views/pages"
)

const internalErrorMessage = "Internal server error"

const safeErrorHeader = "X-DurpDeploy-Safe-Error"

func markSafeErrorResponse(w http.ResponseWriter) {
	w.Header().Set(safeErrorHeader, "true")
}

type internalErrorResponseWriter struct {
	http.ResponseWriter
	status      int
	internalErr bool
	passthrough bool
	body        bytes.Buffer
}

func (w *internalErrorResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		if status >= http.StatusInternalServerError && !w.passthrough {
			w.status = status
			w.internalErr = true
		}
		return
	}
	w.status = status
	w.internalErr = status >= http.StatusInternalServerError
	if !w.internalErr || w.Header().Get(safeErrorHeader) == "true" {
		w.Header().Del(safeErrorHeader)
		w.passthrough = true
		w.internalErr = false
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *internalErrorResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		return w.ResponseWriter.Write(p)
	}
	return w.body.Write(p)
}

func (w *internalErrorResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *internalErrorResponseWriter) Flush() {
	if !w.passthrough {
		w.passthrough = true
		if w.status == 0 {
			w.status = http.StatusOK
		}
		w.ResponseWriter.WriteHeader(w.status)
		_, _ = w.body.WriteTo(w.ResponseWriter)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// InternalErrorMiddleware prevents unexpected server diagnostics from being
// returned to clients. Handlers may keep their specific 4xx contracts; only
// 5xx responses are replaced at the HTTP boundary.
func InternalErrorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := &internalErrorResponseWriter{ResponseWriter: w}
		next.ServeHTTP(ww, r)
		if ww.passthrough {
			return
		}
		if ww.status == 0 {
			ww.status = http.StatusOK
		}
		if !ww.internalErr {
			w.WriteHeader(ww.status)
			_, _ = ww.body.WriteTo(w)
			return
		}

		slog.Error(
			"internal request error",
			"error", strings.TrimSpace(ww.body.String()),
			"request_id", middleware.GetReqID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
		)

		w.Header().Del("Content-Length")
		w.Header().Del("Content-Encoding")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(ww.status)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": strings.ToLower(internalErrorMessage),
			})
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(ww.status)
		_, _ = w.Write([]byte(internalErrorMessage + "\n"))
	})
}

func IsUniqueViolation(err error) bool {
	return err != nil &&
		strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func WriteFormError(
	w http.ResponseWriter,
	r *http.Request,
	fragment templ.Component,
	page templ.Component,
) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Retarget", "#form-container")
		w.Header().Set("HX-Reswap", "innerHTML")
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := fragment.Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := page.Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

type ErrorHandler struct{}

func NewErrorHandler() *ErrorHandler {
	return &ErrorHandler{}
}

func (h *ErrorHandler) NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	if err := pages.ErrorPage("Not Found", "The page you are looking for does not exist.", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *ErrorHandler) MethodNotAllowed(
	w http.ResponseWriter,
	r *http.Request,
) {
	w.WriteHeader(http.StatusMethodNotAllowed)
	if err := pages.ErrorPage("Method Not Allowed", "The requested method is not allowed for this resource.", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func PanicRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered",
					"error", rec,
					"path", r.URL.Path,
					"method", r.Method,
				)
				if ww.Status() == 0 {
					w.WriteHeader(http.StatusInternalServerError)
					if err := pages.ErrorPage("Internal Server Error", "Something went wrong. Please try again later.", r.URL.Path).
						Render(r.Context(), w); err != nil {
						http.Error(
							w,
							http.StatusText(http.StatusInternalServerError),
							http.StatusInternalServerError,
						)
					}
				}
			}
		}()
		next.ServeHTTP(ww, r)
	})
}
