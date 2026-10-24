package logscrub

import (
	"strings"
	"testing"
)

func FuzzScrubberStreaming(f *testing.F) {
	for _, seed := range []string{
		"ordinary output", "release-secret", "CUSTOM-123\nafter",
		"Bearer synthetic-proof", "password=synthetic", "éé42",
		"private\xffproof", "CUSTOM-one\nCUSTOM-two SECRET", "",
	} {
		f.Add(seed, uint8(1), uint8(0))
		f.Add(seed, uint8(7), uint8(1))
	}
	f.Fuzz(func(t *testing.T, text string, chunk, pattern uint8) {
		if len(text) > 4096 {
			t.Skip()
		}
		patterns := []string{`CUSTOM-[0-9]+`, `CUSTOM-.*SECRET`, `é+[0-9]+`}
		scrubber := NewWithPatterns(
			[]string{"release-secret"},
			[]string{patterns[int(pattern)%len(patterns)]},
		)
		text += " release-secret\n"
		width := 1 + int(chunk)%32
		parts, streamed := fuzzScrubStream(t, scrubber, text, width)
		whole := scrubber.Scrub(text)
		if streamed != whole ||
			strings.Join(scrubber.ScrubParts(parts), "") != whole {
			t.Fatal("chunk boundaries changed secret redaction")
		}
		if strings.Contains(whole, "release-secret") {
			t.Fatal("known synthetic credential survived redaction")
		}
		// Default word-boundary patterns can conservatively over-redact at
		// chunk boundaries. Check credential removal, not identical formatting.
		_, defaults := fuzzScrubStream(t, New(nil),
			"0Bearer synthetic-default-proof\n", width)
		if strings.Contains(defaults, "synthetic-default-proof") {
			t.Fatal("split default-pattern credential survived redaction")
		}
		// Regex treats distinct invalid bytes as the same RuneError. Those
		// aliases need not stream identically; actual credentials must redact.
		malformed := NewWithPatterns([]string{"private\xffproof"}, nil)
		text += " private\xffproof private\ufffdproof\n"
		parts, streamed = fuzzScrubStream(t, malformed, text, width)
		malformedParts := strings.Join(malformed.ScrubParts(parts), "")
		if strings.Contains(streamed, "private\xffproof") ||
			strings.Contains(streamed, "private\ufffdproof") ||
			malformedParts != malformed.Scrub(text) {
			t.Fatal("malformed literal credential survived redaction")
		}
	})
}

func fuzzScrubStream(
	t *testing.T, scrubber *Scrubber, text string, width int,
) ([]string, string) {
	t.Helper()
	var parts []string
	var pending string
	var streamed strings.Builder
	for start := 0; start < len(text); start += width {
		part := text[start:min(start+width, len(text))]
		parts = append(parts, part)
		pending += part
		held := scrubber.PendingBytes(pending)
		if held < 0 || held > len(pending) {
			t.Fatal("invalid pending-byte count")
		}
		end := len(pending) - held
		streamed.WriteString(scrubber.Scrub(pending[:end]))
		pending = pending[end:]
	}
	streamed.WriteString(scrubber.Scrub(pending))
	return parts, streamed.String()
}
