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
