// Package logscrub removes accidental plaintext credentials from log text.
// It does not attempt to detect encoded or intentionally disguised secrets.
package logscrub

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

const replacement = "[REDACTED]"

var commonSecretPatterns = []string{
	`Bearer\s+[A-Za-z0-9._~+/-]+=*`,
	`ghp_[A-Za-z0-9]{36,}`,
	`AKIA[0-9A-Z]{16}`,
	`xox[bap]-[A-Za-z0-9-]+`,
	`(?i:\b(password|token|key)\s*=\s*[^\s"']+)`,
}

func init() {
	if extra := os.Getenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS"); extra != "" {
		for _, pattern := range strings.Split(extra, ",") {
			if pattern = strings.TrimSpace(pattern); pattern != "" {
				commonSecretPatterns = append(commonSecretPatterns, pattern)
			}
		}
	}
}

// Scrubber protects against accidental plaintext exposure. It matches known
// literal secret values and common credential formats, but not transformed or
// intentionally disguised data such as Base64 or decorated fragments.
type Scrubber struct {
	all      *regexp.Regexp
	literals []string
}

func New(secrets []string) *Scrubber {
	return newScrubber(secrets, commonSecretPatterns)
}

func NewWithPatterns(secrets []string, patterns []string) *Scrubber {
	return newScrubber(secrets, patterns)
}

func newScrubber(
	secrets []string,
	patterns []string,
) *Scrubber {
	literals := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		literals = append(literals, secret)
	}
	sort.Slice(literals, func(i, j int) bool {
		return len(literals[i]) > len(literals[j])
	})
	knownParts := make([]string, len(literals))
	for index, literal := range literals {
		knownParts[index] = regexp.QuoteMeta(literal)
	}
	return &Scrubber{
		all:      compile(append(knownParts, patterns...)),
		literals: literals,
	}
}

func compile(parts []string) *regexp.Regexp {
	if len(parts) == 0 {
		return nil
	}
	compiled, err := regexp.Compile("(?s)(" + strings.Join(parts, "|") + ")")
	if err != nil {
		return nil
	}
	return compiled
}

func (s *Scrubber) Scrub(text string) string {
	if s == nil || s.all == nil {
		return text
	}
	return s.all.ReplaceAllString(text, replacement)
}

// ScrubParts redacts matches across chunk boundaries while preserving
// the number and order of chunks. A cross-boundary match emits one marker in
// the chunk where the match starts and removes the matched bytes elsewhere.
func (s *Scrubber) ScrubParts(parts []string) []string {
	result := append([]string(nil), parts...)
	if s == nil || s.all == nil || len(parts) == 0 {
		return result
	}
	joined := strings.Join(parts, "")
	matches := s.all.FindAllStringIndex(joined, -1)
	if len(matches) == 0 {
		return result
	}
	offset := 0
	for index, part := range parts {
		start, end := offset, offset+len(part)
		var output strings.Builder
		cursor := start
		for _, match := range matches {
			if match[1] <= start || match[0] >= end {
				continue
			}
			left := max(match[0], start)
			if left > cursor {
				output.WriteString(joined[cursor:left])
			}
			if match[0] >= start {
				output.WriteString(replacement)
			}
			cursor = min(match[1], end)
		}
		if cursor < end {
			output.WriteString(joined[cursor:end])
		}
		result[index] = output.String()
		offset = end
	}
	return result
}

func (s *Scrubber) PendingBytes(text string) int {
	if s == nil || text == "" {
		return 0
	}
	pending := 0
	for _, literal := range s.literals {
		limit := min(len(text), len(literal)-1)
		for size := limit; size > pending; size-- {
			if strings.HasSuffix(text, literal[:size]) {
				pending = size
				break
			}
		}
	}
	for _, prefix := range []string{
		"Bearer", "ghp_", "AKIA", "xoxb-", "xoxa-", "xoxp-",
	} {
		limit := min(len(text), len(prefix))
		for size := limit; size > pending; size-- {
			if hasWordSuffix(text, prefix[:size]) {
				pending = size
				break
			}
		}
	}
	for _, prefix := range []string{"password", "token", "key"} {
		limit := min(len(text), len(prefix))
		for size := limit; size > pending; size-- {
			if hasWordSuffixFold(text, prefix[:size]) {
				pending = size
				break
			}
		}
	}
	for _, pattern := range commonPendingPatterns {
		if match := pattern.FindStringIndex(text); match != nil {
			pending = max(pending, match[1]-match[0])
		}
	}
	return pending
}

func hasWordSuffixFold(text string, suffix string) bool {
	start := len(text) - len(suffix)
	if start < 0 || !strings.EqualFold(text[start:], suffix) {
		return false
	}
	if start == 0 {
		return true
	}
	previous := text[start-1]
	return !((previous >= 'a' && previous <= 'z') ||
		(previous >= 'A' && previous <= 'Z') ||
		(previous >= '0' && previous <= '9') || previous == '_')
}

func hasWordSuffix(text string, suffix string) bool {
	if !strings.HasSuffix(text, suffix) {
		return false
	}
	start := len(text) - len(suffix)
	if start == 0 {
		return true
	}
	previous := text[start-1]
	return !((previous >= 'a' && previous <= 'z') ||
		(previous >= 'A' && previous <= 'Z') ||
		(previous >= '0' && previous <= '9') || previous == '_')
}

var commonPendingPatterns = []*regexp.Regexp{
	regexp.MustCompile(`Bearer\s+[A-Za-z0-9._~+/-]*$`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]*$`),
	regexp.MustCompile(`AKIA[0-9A-Z]{0,15}$`),
	regexp.MustCompile(`xox[bap]-[A-Za-z0-9-]*$`),
	regexp.MustCompile(
		`(?i:\b(password|token|key)\s*(?:=\s*[^\s"']*)?)$`,
	),
}
