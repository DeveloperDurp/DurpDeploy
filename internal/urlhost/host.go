// Package urlhost classifies destination hosts without DNS lookups.
package urlhost

import (
	"errors"
	"net"
	"strings"
)

var ErrInvalid = errors.New("URL host is not permitted")

// Parse returns a comparison name and an optional canonical IP literal.
// It never changes the URL used for source identity or delivery.
func Parse(host string) (string, net.IP, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.Contains(host, "%") {
		return "", nil, ErrInvalid
	}
	if ip := net.ParseIP(host); ip != nil {
		return host, ip, nil
	}
	if strings.Contains(host, ":") || numericHost(host) {
		return "", nil, ErrInvalid
	}
	return host, nil, nil
}

// numericHost recognizes ambiguous numeric spellings, not DNS aliases.
func numericHost(host string) bool {
	for _, part := range strings.Split(host, ".") {
		digits := "0123456789"
		if strings.HasPrefix(part, "0x") {
			part = part[2:]
			digits += "abcdef"
		}
		if part == "" || strings.Trim(part, digits) != "" {
			return false
		}
	}
	return true
}
