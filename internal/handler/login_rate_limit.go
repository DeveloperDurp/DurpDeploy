package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"durpdeploy/internal/requestmeta"
)

const (
	loginLimitWindow  = 15 * time.Minute
	loginIPLimit      = 30
	loginPairLimit    = 5
	mfaIPLimit        = 20
	oidcIPLimit       = 20
	maxLoginLimitKeys = 10000
)

type loginLimitEntry struct {
	started time.Time
	count   int
}

type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]loginLimitEntry
	now     func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		entries: make(map[string]loginLimitEntry),
		now:     time.Now,
	}
}

func (l *loginLimiter) allow(key string, limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	entry, exists := l.entries[key]
	if !exists || now.Sub(entry.started) >= loginLimitWindow {
		if len(l.entries) >= maxLoginLimitKeys {
			for existingKey, existing := range l.entries {
				if now.Sub(existing.started) >= loginLimitWindow {
					delete(l.entries, existingKey)
				}
			}
			if len(l.entries) >= maxLoginLimitKeys {
				return false
			}
		}
		l.entries[key] = loginLimitEntry{started: now, count: 1}
		return true
	}
	if entry.count >= limit {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func loginPairKey(email, ip string) string {
	sum := sha256.Sum256([]byte(
		strings.ToLower(strings.TrimSpace(email)) + "\x00" + ip,
	))
	return "login-pair:" + hex.EncodeToString(sum[:])
}

func (h *AuthHandler) publicRateLimit(
	scope string,
	limit int,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := requestmeta.ClientIP(r)
			if !h.loginLimiter.allow(scope+":"+ip, limit) {
				slog.Warn(
					"authentication request throttled",
					"surface", scope,
					"ip", ip,
				)
				w.Header().Set("Retry-After", "900")
				http.Error(
					w,
					"Authentication is temporarily unavailable",
					http.StatusTooManyRequests,
				)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (h *AuthHandler) MFARateLimit(next http.Handler) http.Handler {
	return h.publicRateLimit("mfa", mfaIPLimit)(next)
}

func (h *AuthHandler) OIDCRateLimit(next http.Handler) http.Handler {
	return h.publicRateLimit("oidc", oidcIPLimit)(next)
}
