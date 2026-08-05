package notify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var errBlockedNotificationHost = errors.New("notification URL host is blocked")

// ValidateEndpointURL rejects blank-tolerant notification endpoints that can
// trivially target local services. Delivery uses NewHTTPClient too, so DNS names
// that resolve privately later are still blocked at dial time.
func ValidateEndpointURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	if u.Host == "" || u.User != nil {
		return fmt.Errorf("URL must include a host and no userinfo")
	}
	host := u.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") ||
		strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return errBlockedNotificationHost
	}
	if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) {
		return errBlockedNotificationHost
	}
	return nil
}

func NewHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(
		ctx context.Context,
		network string,
		address string,
	) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !isPublicIP(ip) {
				return nil, errBlockedNotificationHost
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: transport}
}

func isPublicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified()
}
