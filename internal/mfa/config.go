package mfa

import (
	"errors"
	"net"
	"net/url"
	"os"
)

var ErrInvalidPublicURL = errors.New("invalid DURPDEPLOY_URL")

type Config struct {
	WebAuthn     WebAuthnConfig
	CookieSecure bool
}

type WebAuthnConfig struct {
	Enabled bool
	Origin  string
	RPID    string
}

func LoadConfig() (Config, error) {
	configuredURL := os.Getenv("DURPDEPLOY_URL")
	if configuredURL == "" {
		return Config{}, nil
	}

	parsed, err := url.Parse(configuredURL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" ||
		parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" {
		return Config{}, ErrInvalidPublicURL
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return Config{}, ErrInvalidPublicURL
	}

	switch parsed.Scheme {
	case "https":
		return Config{
			WebAuthn: WebAuthnConfig{
				Enabled: true,
				Origin:  parsed.Scheme + "://" + parsed.Host,
				RPID:    hostname,
			},
			CookieSecure: true,
		}, nil
	case "http":
		if hostname != "localhost" && !isLoopback(hostname) {
			return Config{}, ErrInvalidPublicURL
		}
		return Config{
			WebAuthn: WebAuthnConfig{
				Enabled: true,
				Origin:  parsed.Scheme + "://" + parsed.Host,
				RPID:    hostname,
			},
		}, nil
	default:
		return Config{}, ErrInvalidPublicURL
	}
}

func isLoopback(hostname string) bool {
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}
