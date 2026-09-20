package repository

import (
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/logscrub"
)

func TestPrepareRemoteLogEventsAcceptsInitialSequenceZero(t *testing.T) {
	ready, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{{Sequence: 0, Line: "safe"}},
		-1,
		logscrub.New([]string{"top-secret"}),
		false,
	)
	if err != nil || len(ready) != 1 || len(pending) != 0 {
		t.Fatalf("ready=%+v pending=%+v error=%v", ready, pending, err)
	}
}

func TestPrepareRemoteLogEventsHoldsRetransmittedInitialGapUntilFlush(
	t *testing.T,
) {
	scrubber := logscrub.New(nil)
	ready, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{{Sequence: 7, Line: "safe"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(ready) != 0 || len(pending) != 1 {
		t.Fatalf("first ready=%+v pending=%+v error=%v", ready, pending, err)
	}
	ready, pending, err = prepareRemoteLogEvents(
		pending,
		[]RemoteLogEvent{{Sequence: 7, Line: "safe"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(ready) != 0 || len(pending) != 1 {
		t.Fatalf("retry ready=%+v pending=%+v error=%v", ready, pending, err)
	}
	ready, pending, err = prepareRemoteLogEvents(
		pending, nil, -1, scrubber, true,
	)
	if err != nil || len(ready) != 1 || len(pending) != 0 {
		t.Fatalf("flush ready=%+v pending=%+v error=%v", ready, pending, err)
	}
}

func TestPrepareRemoteLogEventsRedactsSplitCommonPattern(t *testing.T) {
	scrubber := logscrub.New(nil)
	ready, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{{Sequence: 1, Line: "AKIAABCDEFGH"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(ready) != 0 || len(pending) != 1 {
		t.Fatalf("first ready=%+v pending=%+v error=%v", ready, pending, err)
	}
	ready, pending, err = prepareRemoteLogEvents(
		pending,
		[]RemoteLogEvent{{Sequence: 2, Line: "IJKLMNOP"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(ready) != 2 || len(pending) != 0 ||
		ready[0].Line+ready[1].Line != "[REDACTED]" {
		t.Fatalf("second ready=%+v pending=%+v error=%v", ready, pending, err)
	}
}

func TestPrepareRemoteLogEventsRedactsSplitConfiguredPattern(t *testing.T) {
	scrubber := logscrub.NewWithPatterns(nil, []string{`CUSTOM-[0-9]+`})
	ready, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{{Sequence: 1, Line: "CUSTOM-"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(ready) != 0 || len(pending) != 1 {
		t.Fatalf("first ready=%+v pending=%+v error=%v", ready, pending, err)
	}
	ready, pending, err = prepareRemoteLogEvents(
		pending,
		[]RemoteLogEvent{{Sequence: 2, Line: "123\n"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(ready) != 2 || len(pending) != 0 ||
		ready[0].Line+ready[1].Line != "[REDACTED]\n" {
		t.Fatalf("second ready=%+v pending=%+v error=%v", ready, pending, err)
	}
}

func TestPrepareRemoteLogEventsDrainsAfterConfiguredMatch(t *testing.T) {
	scrubber := logscrub.NewWithPatterns(nil, []string{`CUSTOM-[0-9]+`})
	_, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{{Sequence: 1, Line: "CUSTOM-"}},
		-1,
		scrubber,
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	ready, pending, err := prepareRemoteLogEvents(
		pending,
		[]RemoteLogEvent{{Sequence: 2, Line: "123\n"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(ready) != 2 || len(pending) != 0 {
		t.Fatalf("match ready=%+v pending=%+v error=%v", ready, pending, err)
	}
	line := strings.Repeat("x", 1024) + "\n"
	for sequence := int64(3); sequence <= 300; sequence++ {
		ready, pending, err = prepareRemoteLogEvents(
			pending,
			[]RemoteLogEvent{{Sequence: sequence, Line: line}},
			sequence-1,
			scrubber,
			false,
		)
		if err != nil || len(ready) != 1 || len(pending) != 0 {
			t.Fatalf(
				"sequence=%d ready=%d pending=%d error=%v",
				sequence,
				len(ready),
				len(pending),
				err,
			)
		}
	}
}

func TestPrepareRemoteLogEventsHoldsFromFirstConfiguredPrefix(t *testing.T) {
	scrubber := logscrub.NewWithPatterns(nil, []string{`CUSTOM-.*SECRET`})
	ready, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{
			{Sequence: 1, Line: "CUSTOM-one\n"},
			{Sequence: 2, Line: "CUSTOM-two"},
		},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(ready) != 0 || len(pending) != 2 {
		t.Fatalf("ready=%+v pending=%+v error=%v", ready, pending, err)
	}
}

func TestPrepareRemoteLogEventsBoundsSequenceGap(t *testing.T) {
	_, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{{Sequence: 2, Line: "Bearer waiting"}},
		-1,
		logscrub.New(nil),
		false,
	)
	if err != nil || len(pending) != 1 {
		t.Fatalf("initial pending=%+v error=%v", pending, err)
	}
	_, _, err = prepareRemoteLogEvents(
		pending,
		[]RemoteLogEvent{{Sequence: 102, Line: "too far"}},
		-1,
		logscrub.New(nil),
		false,
	)
	if !errors.Is(err, ErrRemoteLifecycleConflict) {
		t.Fatalf("gap error=%v", err)
	}
}

func TestPrepareRemoteLogEventsHoldsUnboundedTokenUntilFlush(t *testing.T) {
	scrubber := logscrub.New(nil)
	_, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{{Sequence: 1, Line: "Bearer abc"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(pending) != 1 {
		t.Fatalf("first pending=%+v error=%v", pending, err)
	}
	_, pending, err = prepareRemoteLogEvents(
		pending,
		[]RemoteLogEvent{{Sequence: 2, Line: "def"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(pending) != 2 {
		t.Fatalf("second pending=%+v error=%v", pending, err)
	}
	ready, pending, err := prepareRemoteLogEvents(
		pending, nil, -1, scrubber, true,
	)
	if err != nil || len(pending) != 0 || len(ready) != 2 ||
		ready[0].Line+ready[1].Line != "[REDACTED]" {
		t.Fatalf("flush ready=%+v pending=%+v error=%v", ready, pending, err)
	}
}

func TestPrepareRemoteLogEventsRedactsSplitAssignmentName(t *testing.T) {
	scrubber := logscrub.New(nil)
	_, pending, err := prepareRemoteLogEvents(
		nil,
		[]RemoteLogEvent{{Sequence: 1, Line: "PASS"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(pending) != 1 {
		t.Fatalf("first pending=%+v error=%v", pending, err)
	}
	_, pending, err = prepareRemoteLogEvents(
		pending,
		[]RemoteLogEvent{{Sequence: 2, Line: "WORD=secret"}},
		-1,
		scrubber,
		false,
	)
	if err != nil || len(pending) != 2 {
		t.Fatalf("second pending=%+v error=%v", pending, err)
	}
	ready, _, err := prepareRemoteLogEvents(
		pending, nil, -1, scrubber, true,
	)
	if err != nil || len(ready) != 2 ||
		ready[0].Line+ready[1].Line != "[REDACTED]" {
		t.Fatalf("flush ready=%+v error=%v", ready, err)
	}
}
