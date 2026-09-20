// Package logscrub removes accidental plaintext credentials from log text.
// It does not attempt to detect encoded or intentionally disguised secrets.
package logscrub

import (
	"os"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"unicode/utf8"
)

const replacement = "[REDACTED]"

var commonSecretPatterns = []string{
	`Bearer\s+[A-Za-z0-9._~+/-]+=*`,
	`ghp_[A-Za-z0-9]{36,}`,
	`AKIA[0-9A-Z]{16}`,
	`xox[bap]-[A-Za-z0-9-]+`,
	`(?i:\b(password|token|key)\s*=\s*[^\s"']+)`,
}

var extraSecretPatterns []string

func init() {
	if extra := os.Getenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS"); extra != "" {
		for _, pattern := range strings.Split(extra, ",") {
			if pattern = strings.TrimSpace(pattern); pattern != "" {
				extraSecretPatterns = append(extraSecretPatterns, pattern)
			}
		}
	}
}

// Scrubber protects against accidental plaintext exposure. It matches known
// literal secret values and common credential formats, but not transformed or
// intentionally disguised data such as Base64 or decorated fragments.
type Scrubber struct {
	all             *regexp.Regexp
	literals        []string
	pendingPatterns []pendingPattern
}

type pendingPattern struct {
	program *syntax.Prog
}

func New(secrets []string) *Scrubber {
	return newScrubber(secrets, commonSecretPatterns, extraSecretPatterns)
}

func NewWithPatterns(secrets []string, patterns []string) *Scrubber {
	return newScrubber(secrets, nil, patterns)
}

func newScrubber(
	secrets []string,
	patterns []string,
	streamPatterns []string,
) *Scrubber {
	literals := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		literals = append(literals, secret)
	}
	effectivePatterns := append([]string(nil), patterns...)
	pendingPatterns := make([]pendingPattern, 0, len(streamPatterns))
	for _, pattern := range streamPatterns {
		expression, err := syntax.Parse("(?s)("+pattern+")", syntax.Perl)
		if err != nil {
			continue
		}
		dropEmptyWidthAssertions(expression)
		pattern = expression.String()
		effectivePatterns = append(effectivePatterns, pattern)
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		prefix, complete := compiled.LiteralPrefix()
		if complete {
			literals = append(literals, prefix)
			continue
		}
		program, err := syntax.Compile(expression.Simplify())
		if err != nil {
			continue
		}
		pendingPatterns = append(pendingPatterns, pendingPattern{
			program: program,
		})
	}
	sort.Slice(literals, func(i, j int) bool {
		return len(literals[i]) > len(literals[j])
	})
	knownParts := make([]string, len(literals))
	for index, literal := range literals {
		knownParts[index] = regexp.QuoteMeta(literal)
	}
	return &Scrubber{
		all:             compile(append(knownParts, effectivePatterns...)),
		literals:        literals,
		pendingPatterns: pendingPatterns,
	}
}

func dropEmptyWidthAssertions(expression *syntax.Regexp) {
	switch expression.Op {
	case syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		expression.Op = syntax.OpEmptyMatch
		expression.Sub = nil
		expression.Rune = nil
	}
	for _, subexpression := range expression.Sub {
		dropEmptyWidthAssertions(subexpression)
	}
}

func compile(parts []string) *regexp.Regexp {
	valid := make([]string, 0, len(parts))
	var compiled *regexp.Regexp
	for _, part := range parts {
		candidate := append(valid, part)
		combined, err := regexp.Compile(
			"(?s)(" + strings.Join(candidate, "|") + ")",
		)
		if err == nil {
			valid = candidate
			compiled = combined
		}
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
	incompleteRune := incompleteUTF8SuffixBytes(text)
	pending := incompleteRune
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
	for _, pattern := range s.pendingPatterns {
		patternPending := pattern.pendingBytes(
			text[:len(text)-incompleteRune],
		)
		if patternPending > 0 {
			patternPending += incompleteRune
		}
		pending = max(pending, patternPending)
	}
	if pending > 0 && s.all != nil {
		boundary := len(text) - pending
		for _, match := range s.all.FindAllStringIndex(text, -1) {
			if match[0] < boundary && match[1] > boundary {
				pending = len(text) - match[0]
				break
			}
		}
	}
	return pending
}

func incompleteUTF8SuffixBytes(text string) int {
	limit := max(0, len(text)-utf8.UTFMax+1)
	for start := len(text) - 1; start >= limit; start-- {
		if !utf8.RuneStart(text[start]) {
			continue
		}
		if !utf8.FullRuneInString(text[start:]) {
			return len(text) - start
		}
		return 0
	}
	return 0
}

func (p pendingPattern) pendingBytes(text string) int {
	raw := make(map[uint32]int)
	expanded := make(map[uint32]int)
	nextRaw := make(map[uint32]int)
	previousRune := rune(-1)
	for offset := 0; offset < len(text); {
		recordPatternState(raw, uint32(p.program.Start), offset)
		r, size := utf8.DecodeRuneInString(text[offset:])
		clear(expanded)
		for pc, start := range raw {
			p.addClosure(expanded, pc, start, previousRune, r, true)
		}
		clear(nextRaw)
		for pc, start := range expanded {
			instruction := &p.program.Inst[pc]
			if instruction.MatchRune(r) {
				recordPatternState(nextRaw, instruction.Out, start)
			}
		}
		offset += size
		previousRune = r
		raw, nextRaw = nextRaw, raw
	}
	clear(expanded)
	for pc, start := range raw {
		p.addClosure(expanded, pc, start, previousRune, -1, false)
	}
	pending := 0
	for pc, start := range expanded {
		switch p.program.Inst[pc].Op {
		case syntax.InstRune, syntax.InstRune1,
			syntax.InstRuneAny, syntax.InstRuneAnyNotNL,
			syntax.InstEmptyWidth:
			pending = max(pending, len(text)-start)
		}
	}
	return pending
}

func (p pendingPattern) addClosure(
	states map[uint32]int,
	pc uint32,
	start int,
	previousRune rune,
	nextRune rune,
	resolveEmpty bool,
) {
	if previous, ok := states[pc]; ok && previous <= start {
		return
	}
	states[pc] = start
	instruction := &p.program.Inst[pc]
	switch instruction.Op {
	case syntax.InstAlt, syntax.InstAltMatch:
		p.addClosure(
			states, instruction.Out, start,
			previousRune, nextRune, resolveEmpty,
		)
		p.addClosure(
			states, instruction.Arg, start,
			previousRune, nextRune, resolveEmpty,
		)
	case syntax.InstCapture, syntax.InstNop:
		p.addClosure(
			states, instruction.Out, start,
			previousRune, nextRune, resolveEmpty,
		)
	case syntax.InstEmptyWidth:
		context := syntax.EmptyOpContext(previousRune, nextRune)
		required := syntax.EmptyOp(instruction.Arg)
		if !resolveEmpty || context&required == required {
			p.addClosure(
				states, instruction.Out, start,
				previousRune, nextRune, resolveEmpty,
			)
		}
	}
}

func recordPatternState(states map[uint32]int, pc uint32, start int) {
	if previous, ok := states[pc]; !ok || start < previous {
		states[pc] = start
	}
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
