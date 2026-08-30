package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"
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
	mu             sync.Mutex
	entries        map[string]loginLimitEntry
	trustedProxies []netip.Prefix
	now            func() time.Time
}

func newLoginLimiter() *loginLimiter {
	trustedProxies := trustedProxyPrefixes(
		os.Getenv("DURPDEPLOY_TRUSTED_PROXIES"),
	)
	if len(trustedProxies) == 0 {
		trustedProxies = []netip.Prefix{
			netip.MustParsePrefix("127.0.0.0/8"),
			netip.MustParsePrefix("::1/128"),
		}
	}
	return &loginLimiter{
		entries:        make(map[string]loginLimitEntry),
		trustedProxies: trustedProxies,
		now:            time.Now,
	}
}

func trustedProxyPrefixes(value string) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if addr, err := netip.ParseAddr(raw); err == nil {
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			prefixes = append(prefixes, prefix.Masked())
		} else {
			slog.Warn("ignoring invalid trusted proxy", "value", raw)
		}
	}
	return prefixes
}

func (l *loginLimiter) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return "unknown"
	}
	if containsIP(l.trustedProxies, peer) {
		forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(forwarded) - 1; i >= 0; i-- {
			addr, parseErr := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
			if parseErr != nil {
				continue
			}
			if !containsIP(l.trustedProxies, addr) {
				return addr.String()
			}
		}
	}
	return peer.String()
}

func containsIP(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
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
			ip := h.loginLimiter.clientIP(r)
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
