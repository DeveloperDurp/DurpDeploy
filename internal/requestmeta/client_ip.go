package requestmeta

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPKey struct{}

// Middleware resolves the request's client IP once at the HTTP boundary.
// Forwarding headers are used only when the direct peer is trusted.
func Middleware(trustedProxyConfig string) func(http.Handler) http.Handler {
	trustedProxies := trustedProxyPrefixes(trustedProxyConfig)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := resolveClientIP(r, trustedProxies)
			ctx := context.WithValue(r.Context(), clientIPKey{}, clientIP)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClientIP returns the client IP resolved by Middleware. Direct handler tests
// and other callers without the middleware safely fall back to RemoteAddr.
func ClientIP(r *http.Request) string {
	if clientIP, ok := r.Context().Value(clientIPKey{}).(string); ok {
		return clientIP
	}
	return addrString(peerIP(r.RemoteAddr))
}

func trustedProxyPrefixes(value string) []netip.Prefix {
	if strings.TrimSpace(value) == "" {
		return []netip.Prefix{
			netip.MustParsePrefix("127.0.0.0/8"),
			netip.MustParsePrefix("::1/128"),
		}
	}
	var prefixes []netip.Prefix
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if addr, err := netip.ParseAddr(raw); err == nil {
			addr = addr.Unmap()
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

func resolveClientIP(r *http.Request, trustedProxies []netip.Prefix) string {
	peer := peerIP(r.RemoteAddr)
	if !peer.IsValid() || !containsIP(trustedProxies, peer) {
		return addrString(peer)
	}
	values := r.Header.Values("X-Forwarded-For")
	for valueIndex := len(values) - 1; valueIndex >= 0; valueIndex-- {
		addresses := strings.Split(values[valueIndex], ",")
		for addressIndex := len(addresses) - 1; addressIndex >= 0; addressIndex-- {
			addr, err := netip.ParseAddr(
				strings.TrimSpace(addresses[addressIndex]),
			)
			if err != nil {
				return addrString(peer)
			}
			addr = addr.Unmap()
			if !containsIP(trustedProxies, addr) {
				return addr.String()
			}
		}
	}
	return addrString(peer)
}

func addrString(addr netip.Addr) string {
	if !addr.IsValid() {
		return "unknown"
	}
	return addr.String()
}

func peerIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func containsIP(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
