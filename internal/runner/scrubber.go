package runner

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

// commonSecretPatterns match well-known credential formats that show up in
// deploy logs even when they were never registered as a secret variable
// (e.g. a script echoes a token it fetched at runtime).
var commonSecretPatterns = []string{
	`Bearer\s+[A-Za-z0-9._~+/-]+=*`,
	`ghp_[A-Za-z0-9]{36,}`,
	`AKIA[0-9A-Z]{16}`,
	`xox[bap]-[A-Za-z0-9-]+`,
	`(?i:\b(password|token|key)\s*=\s*[^\s"']+)`,
}

func init() {
	if extra := os.Getenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS"); extra != "" {
		// ponytail: comma is the separator. If a regex needs a comma
		// (e.g. {1,2}), this will break it. Use multiple env vars or
		// a different separator if this becomes a problem.
		for _, p := range strings.Split(extra, ",") {
			if p = strings.TrimSpace(p); p != "" {
				commonSecretPatterns = append(commonSecretPatterns, p)
			}
		}
	}
}

// Scrubber redacts known secret values and common credential patterns from
// arbitrary text using a single pre-compiled regex. Redaction is best-effort:
// it catches literal secret values and a handful of common token formats, not
// every possible secret shape.
type Scrubber struct {
	re *regexp.Regexp
}

// NewScrubber compiles a single regex matching every literal secret (longest
// first, so a longer secret is redacted whole rather than leaving a suffix
// behind) plus the common credential patterns. Empty secrets are ignored.
func NewScrubber(secrets []string) *Scrubber {
	literals := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s == "" {
			continue
		}
		literals = append(literals, s)
	}
	sort.Slice(literals, func(i, j int) bool {
		return len(literals[i]) > len(literals[j])
	})

	parts := make([]string, 0, len(literals)+len(commonSecretPatterns))
	for _, s := range literals {
		parts = append(parts, regexp.QuoteMeta(s))
	}
	parts = append(parts, commonSecretPatterns...)

	if len(parts) == 0 {
		return &Scrubber{}
	}

	// (?s) so literal secrets containing newlines (e.g. a multi-line SSH
	// key) still match across lines.
	re, err := regexp.Compile("(?s)(" + strings.Join(parts, "|") + ")")
	if err != nil {
		// Should never happen: literals are escaped and the common
		// patterns are static, but fall back to no-op scrubbing rather
		// than panicking on a malformed secret.
		return &Scrubber{}
	}
	return &Scrubber{re: re}
}

// Scrub replaces every match with "[REDACTED]". A zero-value Scrubber (no
// secrets, no patterns) is a no-op.
func (s *Scrubber) Scrub(text string) string {
	if s == nil || s.re == nil {
		return text
	}
	return s.re.ReplaceAllString(text, "[REDACTED]")
}
