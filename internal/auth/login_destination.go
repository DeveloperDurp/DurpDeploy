package auth

import (
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"
)

// SafeLoginDestination accepts bounded local pages after authentication.
func SafeLoginDestination(value string) string {
	if len(value) > 2048 {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil ||
		!strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return "/"
	}
	if strings.HasPrefix(parsed.Path, "//") ||
		strings.Contains(parsed.Path, `\`) ||
		strings.IndexFunc(parsed.Path, unicode.IsControl) >= 0 {
		return "/"
	}
	cleanPath := path.Clean(parsed.Path)
	if cleanPath == "/login" || strings.HasPrefix(cleanPath, "/login/") ||
		cleanPath == "/logout" {
		return "/"
	}
	destination := parsed.RequestURI()
	if len(destination) > 2048 {
		return "/"
	}
	return destination
}

func loginRedirectURL(r *http.Request) string {
	destination := "/"
	if r.Method == http.MethodGet {
		destination = r.URL.RequestURI()
	}
	if r.Header.Get("HX-Request") == "true" &&
		r.Header.Get("HX-Boosted") != "true" &&
		r.Header.Get("HX-History-Restore-Request") != "true" {
		destination = "/"
		current, err := url.Parse(r.Header.Get("HX-Current-URL"))
		if err == nil && current.Host == r.Host &&
			(current.Scheme == "https" || current.Scheme == "http") {
			destination = current.RequestURI()
		}
	}
	destination = SafeLoginDestination(destination)
	if destination == "/" {
		return "/login"
	}
	return "/login?" + url.Values{"next": {destination}}.Encode()
}
