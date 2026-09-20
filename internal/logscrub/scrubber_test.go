package logscrub

import (
	"reflect"
	"testing"
)

func TestScrubPartsRedactsOneLiteralAcrossChunks(t *testing.T) {
	scrubber := New([]string{"top-secret"})
	got := scrubber.ScrubParts([]string{"prefix top", "-sec", "ret suffix"})
	want := []string{"prefix [REDACTED]", "", " suffix"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScrubKnownParts()=%q want=%q", got, want)
	}
}

func TestPendingBytesDoesNotHoldCredentialNameSuffixInWord(t *testing.T) {
	if got := New(nil).PendingBytes("monkey"); got != 0 {
		t.Fatalf("PendingBytes(monkey)=%d want=0", got)
	}
}

func TestPendingBytesHoldsCaseInsensitiveAssignmentPrefix(t *testing.T) {
	if got := New(nil).PendingBytes("prefix PASS"); got != 4 {
		t.Fatalf("PendingBytes(prefix PASS)=%d want=4", got)
	}
}

func TestPendingBytesHoldsConfiguredPatternPrefix(t *testing.T) {
	scrubber := NewWithPatterns(nil, []string{`CUSTOM-[0-9]+`})
	for _, text := range []string{"CUSTOM-", "CUSTOM-1", "CUSTOM-123"} {
		if got := scrubber.PendingBytes("prefix " + text); got != len(text) {
			t.Errorf("PendingBytes(%q)=%d want=%d", text, got, len(text))
		}
	}
	if got := scrubber.PendingBytes("CUSTOM-123 done"); got != 0 {
		t.Errorf("PendingBytes(terminated match)=%d want=0", got)
	}
}

func TestPendingBytesHoldsConfiguredPatternsWithoutSafePrefix(t *testing.T) {
	tests := []struct {
		pattern string
		text    string
		want    int
	}{
		{`CUSTOM-[0-9]{3}`, "CUSTOM-1", len("CUSTOM-1")},
		{`(foo|bar)[0-9]+`, "safe foo1", len("foo1")},
	}
	for _, test := range tests {
		scrubber := NewWithPatterns(nil, []string{test.pattern})
		if got := scrubber.PendingBytes(test.text); got != test.want {
			t.Errorf(
				"pattern %q: PendingBytes()=%d want=%d",
				test.pattern,
				got,
				test.want,
			)
		}
	}
}

func TestPendingBytesHoldsMultilineConfiguredPattern(t *testing.T) {
	scrubber := NewWithPatterns(nil, []string{"CUSTOM-\\n[0-9]+"})
	if got := scrubber.PendingBytes(
		"prefix CUSTOM-\n",
	); got != len("CUSTOM-\n") {
		t.Fatalf("PendingBytes()=%d want=%d", got, len("CUSTOM-\n"))
	}
}

func TestPendingBytesHoldsFromFirstRepeatedConfiguredPrefix(t *testing.T) {
	scrubber := NewWithPatterns(nil, []string{`CUSTOM-.*SECRET`})
	text := "CUSTOM-one\nCUSTOM-two"
	if got := scrubber.PendingBytes(text); got != len(text) {
		t.Fatalf("PendingBytes()=%d want=%d", got, len(text))
	}
}

func TestConfiguredPatternsIgnoreChunkSensitiveBoundaries(t *testing.T) {
	nonBoundary := NewWithPatterns(nil, []string{`foo\B`})
	if got := nonBoundary.Scrub("foo "); got != "[REDACTED] " {
		t.Errorf("Scrub(foo\\B)=%q", got)
	}

	wordBoundary := NewWithPatterns(nil, []string{`TOKEN\b`})
	if got := wordBoundary.Scrub("TOKENX"); got != "[REDACTED]X" {
		t.Errorf("Scrub(TOKEN\\b)=%q", got)
	}
}

func TestPendingBytesHoldsNormalizedLeadingBoundaryPattern(t *testing.T) {
	scrubber := NewWithPatterns(nil, []string{`\b-[A-Z]+`})
	for _, text := range []string{"-", "-T", "-TOKEN"} {
		if got := scrubber.PendingBytes(text); got != len(text) {
			t.Errorf("PendingBytes(%q)=%d want=%d", text, got, len(text))
		}
	}
	if got := scrubber.PendingBytes("-TOKEN "); got != len("-TOKEN ") {
		t.Errorf("PendingBytes(overlap)=%d want=%d", got, len("-TOKEN "))
	}
	if got := scrubber.PendingBytes("-TOKEN d"); got != 0 {
		t.Errorf("PendingBytes(terminated)=%d want=0", got)
	}
}

func TestPendingBytesPreservesWholeStreamRedaction(t *testing.T) {
	tests := []struct {
		pattern string
		text    string
	}{
		{`CUSTOM-[0-9]+`, "before CUSTOM-123\nafter"},
		{`CUSTOM-[0-9]{3}`, "CUSTOM-1234"},
		{`CUSTOM-(foo)?`, "CUSTOM-foo done"},
		{`(foo|bar)[0-9]+`, "safe bar42 done"},
		{`CUSTOM-.*SECRET`, "CUSTOM-one\nCUSTOM-two SECRET\nafter"},
		{`(?m)^TOKEN=[^\n]+$`, "before\nTOKEN=secret\nafter"},
		{`\bTOKEN\b`, "before TOKEN after"},
		{`foo\B`, "before foox after"},
		{`\BTOKEN[0-9]+`, "aTOKEN42 done"},
		{`\b-[A-Z]+`, "a-TOKEN done"},
		{`é+[0-9]+`, "before éé42 after"},
	}
	for _, test := range tests {
		scrubber := NewWithPatterns(nil, []string{test.pattern})
		var emitted, pending string
		for _, next := range []byte(test.text) {
			pending += string([]byte{next})
			pendingBytes := scrubber.PendingBytes(pending)
			safeEnd := len(pending) - pendingBytes
			emitted += scrubber.Scrub(pending[:safeEnd])
			pending = pending[safeEnd:]
		}
		got := emitted + scrubber.Scrub(pending)
		want := scrubber.Scrub(test.text)
		if got != want {
			t.Errorf("pattern %q: streamed=%q want=%q", test.pattern, got, want)
		}
	}
}

func TestMalformedPatternDoesNotDisableValidRedaction(t *testing.T) {
	scrubber := NewWithPatterns(
		[]string{"release-secret"},
		[]string{`Bearer\s+[A-Za-z0-9]+`, `[`},
	)
	got := scrubber.Scrub("release-secret Bearer token")
	if got != "[REDACTED] [REDACTED]" {
		t.Fatalf("Scrub()=%q; malformed pattern disabled valid redaction", got)
	}
}

func TestAllInvalidPatternsAreInert(t *testing.T) {
	scrubber := NewWithPatterns(nil, []string{`[`, `(?P<`})
	if got := scrubber.Scrub("plain text"); got != "plain text" {
		t.Fatalf("Scrub()=%q want unchanged text", got)
	}
}
