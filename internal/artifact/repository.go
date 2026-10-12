// Package artifact fetches and validates pinned generic ZIP packages.
package artifact

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
)

const (
	versionPlaceholder = "{version}"
	MaxDownload        = agentartifact.MaxDownload
	MaxExtracted       = agentartifact.MaxExtracted
	MaxFiles           = agentartifact.MaxFiles
	// One additional inode is needed for the staging mount's root.
	MaxStagingNodes = agentartifact.MaxStagingNodes
	MaxMetadata     = agentartifact.MaxMetadata
	PathVariable    = agentartifact.PathVariable
)

var (
	ErrInvalid       = agentartifact.ErrInvalid
	ErrFetch         = errors.New("artifact download failed")
	ErrNotFound      = fmt.Errorf("artifact package not found: %w", ErrFetch)
	ErrChecksum      = agentartifact.ErrChecksum
	ErrMetadataLimit = agentartifact.ErrMetadataLimit
)

type Repository struct {
	URLTemplate string `json:"url_template"`
	AuthType    string `json:"auth_type"`
	Username    string `json:"username"`
	Credential  string `json:"-"`
}

func (r Repository) Validate() error {
	if strings.Count(r.URLTemplate, versionPlaceholder) != 1 {
		return ErrInvalid
	}
	if _, err := r.URL("1.0"); err != nil {
		return err
	}
	switch r.AuthType {
	case "noauth":
		if r.Username != "" || r.Credential != "" {
			return ErrInvalid
		}
	case "bearer":
		if r.Username != "" || r.Credential == "" {
			return ErrInvalid
		}
	case "basic":
		if r.Username == "" || r.Credential == "" ||
			strings.Contains(r.Username, ":") {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if strings.ContainsAny(r.Credential+r.Username, "\r\n\x00") {
		return ErrInvalid
	}
	return nil
}

func (r Repository) URL(version string) (string, error) {
	if version == "" || version == "." || version == ".." ||
		strings.ContainsAny(version, "/\\\r\n\x00") {
		return "", ErrInvalid
	}
	raw := strings.ReplaceAll(
		r.URLTemplate,
		versionPlaceholder,
		url.PathEscape(version),
	)
	u, err := url.Parse(raw)
	if err != nil || validURL(u) != nil {
		return "", ErrInvalid
	}
	// Version substitution is confined to the path; it cannot select a host.
	template, err := url.Parse(
		strings.ReplaceAll(r.URLTemplate, versionPlaceholder, "version"),
	)
	if err != nil || template.Host != u.Host ||
		!strings.Contains(template.Path, "version") ||
		u.RawQuery != "" {
		return "", ErrInvalid
	}
	return u.String(), nil
}

func validURL(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.Fragment != "" ||
		u.RawQuery != "" ||
		u.Opaque != "" {
		return ErrInvalid
	}
	return nil
}
