// Package provenance validates public release source identifiers.
package provenance

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"
)

type Source struct {
	RepositoryURL string `json:"repository_url"`
	CommitSHA     string `json:"commit_sha"`
	ArtifactRef   string `json:"artifact_ref"`
	CIRunURL      string `json:"ci_run_url"`
}

var fullSHA = regexp.MustCompile(`^([a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)

var artifactID = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:/-]*(@sha256:[a-fA-F0-9]{64})?$`,
)

var credentials = logscrub.New(
	nil,
	`(?i:glpat-[A-Za-z0-9_-]+|github_pat_[A-Za-z0-9_]+|ddp_pat_[a-f0-9]{64}|gh[sour]_[A-Za-z0-9]{36,})`,
	`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`,
)

func (s Source) Validate() error {
	for _, value := range []string{
		s.RepositoryURL, s.CommitSHA, s.ArtifactRef, s.CIRunURL,
	} {
		if err := validatePublicValue(value); err != nil {
			return err
		}
	}
	if s.CommitSHA != "" && !fullSHA.MatchString(s.CommitSHA) {
		return errors.New(
			"commit_sha must be a full SHA-1 or SHA-256 hexadecimal identifier",
		)
	}
	for _, value := range []string{s.RepositoryURL, s.CIRunURL} {
		if value != "" && !safeURL(value) {
			return errors.New(
				"provenance URLs must use HTTPS without credentials, query parameters, or fragments",
			)
		}
	}
	if strings.Contains(s.ArtifactRef, "://") {
		if !safeURL(s.ArtifactRef) {
			return errors.New(
				"artifact_ref URLs must use HTTPS without credentials, query parameters, or fragments",
			)
		}
	} else if s.ArtifactRef != "" && !artifactID.MatchString(s.ArtifactRef) {
		return errors.New("artifact_ref must be a credential-free artifact identifier")
	}
	return nil
}

func validatePublicValue(value string) error {
	if len(value) > 2048 || !utf8.ValidString(value) ||
		strings.IndexFunc(value, unicode.IsSpace) >= 0 ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return errors.New(
			"provenance fields must be at most 2048 bytes without whitespace or control characters",
		)
	}
	decoded, err := url.PathUnescape(value)
	if err != nil || credentials.Scrub(decoded) != decoded {
		return errors.New("provenance must not contain credentials")
	}
	return nil
}

func safeURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" &&
		u.User == nil && u.Opaque == "" &&
		!strings.ContainsAny(value, "?#\\")
}

func FromRelease(release db.Release) Source {
	return Source{RepositoryURL: release.RepositoryUrl,
		CommitSHA: release.CommitSha, ArtifactRef: release.ArtifactRef,
		CIRunURL: release.CiRunUrl}
}
