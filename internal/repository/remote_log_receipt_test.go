package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"durpdeploy/internal/logscrub"
	"durpdeploy/internal/secret"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRemoteLogReceiptIndependentOfHiddenSecrets(t *testing.T) {
	for _, limit := range []string{"count", "bytes", "gap"} {
		t.Run(limit, func(t *testing.T) {
			matched := remoteLogAdmissionOutcomes(t, limit, "hidden-end")
			unmatched := remoteLogAdmissionOutcomes(t, limit, "unmatched-end")
			if !reflect.DeepEqual(matched, unmatched) {
				t.Fatal("admission depends on hidden literal")
			}
		})
	}
}

func remoteLogAdmissionOutcomes(t *testing.T, limit, literal string) []bool {
	t.Helper()
	var state *remoteLogBuffer
	var accepted []bool
	last := int64(-1)
	count := agentproto.MaxLogEvents + 2
	if limit == "bytes" {
		count = maxRemoteLogBufferBytes/agentproto.MaxLogLineBytes + 2
	}
	for index := 1; index <= count; index++ {
		ready, next, err := prepareRemoteLogEvents(state,
			[]RemoteLogEvent{remoteReceiptEvent(limit, index)},
			last, logscrub.New([]string{literal}), false)
		accepted = append(accepted, err == nil)
		if err != nil {
			continue
		}
		state = next
		if len(ready) > 0 {
			last = ready[len(ready)-1].Sequence
		}
		if !remoteLogEventsFit(append(append([]RemoteLogEvent(nil),
			state.Events.Tail...), state.Events.Gaps...)) {
			t.Fatal("retained raw state exceeded capacity")
		}
	}
	return accepted
}

func remoteReceiptEvent(limit string, index int) RemoteLogEvent {
	line := ""
	if index == 1 {
		line = "hidden-"
	}
	if limit == "bytes" {
		line = strings.Repeat("x", agentproto.MaxLogLineBytes-len(line)) + line
	}
	sequence := int64(index)
	if (limit == "gap" || limit == "bytes") && index > 1 {
		sequence++
	}
	return RemoteLogEvent{Sequence: sequence, Line: line}
}

func TestRemoteLogReceiptSuppressionSurvivesRestart(t *testing.T) {
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := &Repository{secrets: box}
	scrubber := logscrub.New([]string{"hidden-end"})
	var state *remoteLogBuffer
	last := int64(-1)
	for sequence := int64(1); sequence <= agentproto.MaxLogEvents+1; sequence++ {
		line := ""
		if sequence == 1 {
			line = "hidden-"
		}
		ready, next, err := prepareRemoteLogEvents(
			state,
			[]RemoteLogEvent{
				{Sequence: sequence, Line: line},
			},
			last,
			scrubber,
			false,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(ready) != 0 {
			last = ready[len(ready)-1].Sequence
			assertSuppressedRemoteEvents(t, ready)
		}
		state = reloadRemoteLogReceipt(t, repo, next, last)
	}
	if !state.Suppressed || pendingEventCount(state) != 0 {
		t.Fatal("suppression was not retained with an empty tail")
	}
	ready, next, err := prepareRemoteLogEvents(state, []RemoteLogEvent{
		{Sequence: last, Line: "changed duplicate"},
		{Sequence: last + 1, Line: "end safe text"},
	}, last, scrubber, false)
	if err != nil || len(ready) != 1 || ready[0].Line != "[REDACTED]" ||
		!next.Suppressed {
		t.Fatalf("continuation=%+v state=%+v error=%v", ready, next, err)
	}
	ready, next, err = prepareRemoteLogEvents(next,
		[]RemoteLogEvent{{Sequence: last + 3, Line: "gap continuation"}},
		last+1, scrubber, true)
	if err != nil || next != nil || len(ready) != 1 ||
		ready[0].Line != "[REDACTED]" {
		t.Fatalf("terminal=%+v state=%+v error=%v", ready, next, err)
	}
}

func assertSuppressedRemoteEvents(t *testing.T, events []RemoteLogEvent) {
	t.Helper()
	for _, event := range events {
		if event.Line != "[REDACTED]" {
			t.Fatal("overflow released raw tail")
		}
	}
}

func reloadRemoteLogReceipt(
	t *testing.T, repo *Repository, next *remoteLogBuffer, last int64,
) *remoteLogBuffer {
	t.Helper()
	ciphertext, err := repo.encodeRemoteLogBuffer(next)
	if err != nil || !ciphertext.Valid {
		t.Fatalf("encode: %v", err)
	}
	state, err := repo.decodeRemoteLogBuffer(ciphertext, last)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestRemoteLogReceiptLegacyAndInvalidState(t *testing.T) {
	repo := &Repository{}
	state, err := repo.decodeRemoteLogBuffer(sql.NullString{Valid: true,
		String: `{"events":[{"sequence":1,"line":"hidden-"},{"sequence":3,"line":"first"}]}`}, -1)
	if err != nil || state.ReceivedThrough != 1 ||
		len(state.Events.Tail) != 1 ||
		len(state.Events.Gaps) != 1 {
		t.Fatalf("legacy state=%+v error=%v", state, err)
	}
	ready, next, err := prepareRemoteLogEvents(state, []RemoteLogEvent{
		{Sequence: 1, Line: "changed"}, {Sequence: 2, Line: "end\n"},
		{Sequence: 3, Line: "changed gap"},
	}, -1, logscrub.New([]string{"hidden-end"}), false)
	if err != nil || len(ready) != 3 || ready[2].Line != "first" ||
		next.ReceivedThrough != 3 {
		t.Fatalf("first accepted content lost: %+v %v", ready, err)
	}
	encoded, err := repo.encodeRemoteLogBuffer(next)
	if err != nil {
		t.Fatal(err)
	}
	var old struct{ Events []RemoteLogEvent }
	if json.Unmarshal([]byte(encoded.String), &old) == nil {
		t.Fatal("old decoder accepted v2 state")
	}
	for _, raw := range []string{
		`{}`, `null`, `{"events":{}}`,
		`{"version":null,"events":[]}`,
		`{"version":3,"received_through":-1,"suppressed":false,"events":{}}`,
		`{"version":2,"suppressed":false,"events":{}}`,
		`{"version":2,"received_through":1,"suppressed":true,"events":{"tail":[{"sequence":1,"line":"raw"}]}}`,
		`{"version":2,"received_through":2,"suppressed":false,"events":{"tail":[],"gaps":[]}}`,
		`{"version":2,"received_through":-1,"suppressed":false,"events":{"gaps":[{"sequence":-1}]}}`,
	} {
		if _, err := repo.decodeRemoteLogBuffer(sql.NullString{String: raw, Valid: true}, -1); err == nil {
			t.Fatalf("invalid state accepted: %s", raw)
		}
	}
}

func TestRemoteLogReceiptIntegerBoundary(t *testing.T) {
	for _, cursor := range []int64{-1, 0, math.MaxInt64 - 1, math.MaxInt64} {
		t.Run(fmt.Sprint(cursor), func(t *testing.T) {
			state := &remoteLogBuffer{Version: 2, ReceivedThrough: cursor}
			_, next, err := prepareRemoteLogEvents(state,
				[]RemoteLogEvent{{Sequence: math.MaxInt64, Line: "safe\n"}},
				cursor, logscrub.New(nil), false)
			if cursor < math.MaxInt64-1 {
				if !errors.Is(err, ErrRemoteLifecycleConflict) {
					t.Fatalf("unbounded gap: %v", err)
				}
			} else if err != nil || next.ReceivedThrough != math.MaxInt64 {
				t.Fatalf("maximum sequence: %+v %v", next, err)
			}
		})
	}
}
