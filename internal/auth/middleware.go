package auth

import (
	"context"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type contextKey string

const userKey contextKey = "user"
const sessionKey contextKey = "session"

// SetUser returns a new request with the user stored in context.
func SetUser(r *http.Request, user *db.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userKey, user))
}

// UserFromContext retrieves the user from the request context.
// Returns nil if absent.
func UserFromContext(ctx context.Context) *db.User {
	u, _ := ctx.Value(userKey).(*db.User)
	return u
}

// SessionFromContext retrieves the session from the request context.
// Returns nil if absent.
func SessionFromContext(ctx context.Context) *db.Session {
	s, _ := ctx.Value(sessionKey).(*db.Session)
	return s
}

// AuthMiddleware reads the session cookie, validates it against the DB,
// extends expiry via TouchSession, and injects the user + session into
// request context. Any unauthenticated request is redirected to /login.
//
// Applied only to the protected route group in server.NewRouter; public
// routes (/login, /static/*, /healthz, /logout) are mounted on the root
// mux and never reach this middleware.
func AuthMiddleware(repo *repository.Repository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("session")
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			session, err := repo.Queries.GetSession(r.Context(), db.GetSessionParams{
				ID:        cookie.Value,
				ExpiresAt: time.Now().Unix(),
			})
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			newExpiry := time.Now().Add(24 * time.Hour).Unix()
			_ = repo.Queries.TouchSession(r.Context(), db.TouchSessionParams{
				ExpiresAt: newExpiry,
				ID:        session.ID,
			})

			user := &db.User{
				ID:    session.UserID,
				Email: session.Email,
				Name:  session.Name,
				Role:  session.Role,
			}
			r = SetUser(r, user)

			sess := &db.Session{
				ID:        session.ID,
				UserID:    session.UserID,
				CsrfToken: session.CsrfToken,
				CreatedAt: session.CreatedAt,
				ExpiresAt: session.ExpiresAt,
				IpAddress: session.IpAddress,
				UserAgent: session.UserAgent,
			}
			r = r.WithContext(context.WithValue(r.Context(), sessionKey, sess))

			next.ServeHTTP(w, r)
		})
	}
}
