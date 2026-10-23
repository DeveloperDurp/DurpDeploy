package repository

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/logscrub"
)

func TestPrepareRemoteLogEventsRedactsAdjacentSplitPrefixes(t *testing.T) {
	for _, credential := range []string{
		"Bearer synthetic-proof", "ghp_" + strings.Repeat("S", 36),
		"AKIAABCDEFGHIJKLMNOP", "xoxb-synthetic-proof",
		"xoxa-synthetic-proof", "xoxp-synthetic-proof",
	} {
		for _, leading := range []string{"0", "a", "_", " ", "."} {
			for _, flush := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/flush=%t",
					credential[:4], leading, flush), func(t *testing.T) {
					assertRemotePrefixSplits(t, leading+credential, flush)
				})
			}
		}
	}
}

func assertRemotePrefixSplits(t *testing.T, text string, flush bool) {
	t.Helper()
	if !flush {
		text += "\n"
	}
	for split := 1; split < len(text); split++ {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			assertRemotePrefixSplit(t, text, split, flush)
		})
	}
}

func assertRemotePrefixSplit(
	t *testing.T, text string, split int, flush bool,
) {
	t.Helper()
	scrubber := logscrub.New(nil)
	var pending []RemoteLogEvent
	var emitted string
	last := int64(-1)
	collect := func(events []RemoteLogEvent) {
		t.Helper()
		for _, event := range events {
			if event.Sequence <= last {
				t.Fatal("event order changed")
			}
			last = event.Sequence
			emitted += event.Line
		}
	}
	for index, chunk := range []string{text[:split], text[split:]} {
		ready, remaining, err := prepareRemoteLogEvents(
			pending, []RemoteLogEvent{
				{Sequence: int64(index + 1), Line: chunk},
			}, last, scrubber, false,
		)
		if err != nil {
			t.Fatal(err)
		}
		collect(ready)
		pending = remaining
	}
	if flush {
		ready, remaining, err := prepareRemoteLogEvents(
			pending, nil, last, scrubber, true)
		if err != nil || len(remaining) != 0 {
			t.Fatalf("flush remaining=%d error=%v", len(remaining), err)
		}
		collect(ready)
		pending = remaining
	}
	if len(pending) != 0 {
		t.Fatal("terminated credential did not drain")
	}
	if want := scrubber.Scrub(text); emitted != want {
		t.Fatalf("emitted=%q whole=%q", emitted, want)
	}
}
