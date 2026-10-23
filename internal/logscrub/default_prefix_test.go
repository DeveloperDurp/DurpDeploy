package logscrub

import (
	"fmt"
	"strings"
	"testing"
)

var defaultCredentials = []string{
	"Bearer synthetic-proof",
	"ghp_" + strings.Repeat("S", 36),
	"AKIAABCDEFGHIJKLMNOP",
	"xoxb-synthetic-proof",
	"xoxa-synthetic-proof",
	"xoxp-synthetic-proof",
}

func TestDefaultPrefixesPreserveStreamingRedaction(t *testing.T) {
	for _, credential := range defaultCredentials {
		for _, leading := range []string{"", " ", ".", "0", "a", "Z", "_", "é"} {
			for _, ending := range []string{"", "\n", " done\n"} {
				text := leading + credential + ending
				t.Run(fmt.Sprintf("%s/%q/%q", credential[:4], leading, ending),
					func(t *testing.T) {
						for split := 1; split < len(text); split++ {
							assertDefaultPatternStream(
								t,
								text,
								[]byte{byte(split)},
							)
						}
						assertDefaultPatternStream(t, text, []byte{1})
					})
			}
		}
	}
}

func TestDefaultPrefixBufferingPreservesAssignmentBoundaries(t *testing.T) {
	scrubber := New(nil)
	for _, name := range []string{"password", "token", "key", "PASSword"} {
		for size := 1; size <= len(name); size++ {
			partial := name[:size]
			if got := scrubber.PendingBytes(" " + partial); got != len(
				partial,
			) {
				t.Errorf("assignment prefix %q: pending=%d", partial, got)
			}
		}
		for _, leading := range []string{"0", "a", "_"} {
			text := leading + name + "=ordinary\n"
			if got := scrubber.Scrub(text); got != text {
				t.Errorf("non-boundary assignment changed: %q", got)
			}
			if scrubber.PendingBytes(leading+name) != 0 {
				t.Errorf("non-boundary assignment held: %q", leading+name)
			}
		}
	}
	if got := scrubber.Scrub("monkey=ordinary\n"); got != "monkey=ordinary\n" {
		t.Errorf("word suffix became an assignment: %q", got)
	}
}

// FuzzDefaultPatternStreaming varies only synthetic default-format credentials
// and chunk framing. The broader #48 streaming target remains independent.
func FuzzDefaultPatternStreaming(f *testing.F) {
	for index := range defaultCredentials {
		// The Bearer seed recreates #145's 0Bea / rer synthetic-proof\n case.
		// Original discovery corpus 4bfe6d6a4ac85117 remains owned by #48.
		f.Add(byte(index), byte(3), []byte{4})
	}
	f.Fuzz(func(t *testing.T, kind, adjacent byte, sizes []byte) {
		leading := []string{"", " ", ".", "0", "a", "Z", "_", "é"}
		text := leading[int(adjacent)%len(leading)] +
			defaultCredentials[int(kind)%len(defaultCredentials)] + "\n"
		assertDefaultPatternStream(t, text, sizes[:min(len(sizes), 64)])
	})
}

func assertDefaultPatternStream(t testing.TB, text string, sizes []byte) {
	t.Helper()
	scrubber := New(nil)
	var emitted, pending string
	for index, offset := 0, 0; offset < len(text); index++ {
		size := len(text)
		if len(sizes) != 0 {
			size = max(1, int(sizes[index%len(sizes)]))
		}
		end := min(len(text), offset+size)
		pending += text[offset:end]
		safeEnd := len(pending) - scrubber.PendingBytes(pending)
		emitted += scrubber.Scrub(pending[:safeEnd])
		pending = pending[safeEnd:]
		offset = end
	}
	got := emitted + scrubber.Scrub(pending)
	if want := scrubber.Scrub(text); got != want {
		t.Fatalf("chunks=%v streamed=%q whole=%q", sizes, got, want)
	}
}
