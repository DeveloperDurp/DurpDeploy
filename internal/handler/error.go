package handler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5/middleware"

	"durpdeploy/views/pages"
)

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
