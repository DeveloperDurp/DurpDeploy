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
