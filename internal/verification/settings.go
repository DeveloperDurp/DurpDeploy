package verification

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type Kind string

const (
	None           Kind  = ""
	HTTP           Kind  = "http"
	Bash           Kind  = "bash"
	DefaultTimeout int64 = 30
)

var ErrInvalid = errors.New("invalid verification configuration")

type Settings struct {
	Kind           Kind
	Target         string
	TimeoutSeconds int64
}

func Parse(kind, target string, timeout int64) (Settings, error) {
	settings := Settings{Kind: Kind(kind), Target: target,
		TimeoutSeconds: timeout}
	if settings.TimeoutSeconds == 0 {
		settings.TimeoutSeconds = DefaultTimeout
	}
	if settings.TimeoutSeconds < 1 || settings.TimeoutSeconds > 300 ||
		len(target) > 65536 || strings.ContainsRune(target, '\x00') {
		return Settings{}, ErrInvalid
	}
	switch settings.Kind {
	case None:
		settings.Target = ""
	case HTTP:
		settings.Target = strings.TrimSpace(target)
		if err := validateHTTPURL(settings.Target); err != nil {
			return Settings{}, err
		}
	case Bash:
		if strings.TrimSpace(target) == "" {
			return Settings{}, ErrInvalid
		}
	default:
		return Settings{}, ErrInvalid
	}
	return settings, nil
}

func validateHTTPURL(target string) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
		u.Opaque != "" {
		return ErrInvalid
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil &&
		(!ip.IsGlobalUnicast() || ip.IsLoopback()) {
		return ErrInvalid
	}
	if port := u.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return ErrInvalid
		}
	}
	return nil
}
