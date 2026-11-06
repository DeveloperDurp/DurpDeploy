package repository

import (
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"durpdeploy/internal/logscrub"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

const maxRemoteLogBufferBytes = agentproto.MaxLogBatchBytes

type remoteLogBuffer struct {
	Version         int             `json:"version"`
	ReceivedThrough int64           `json:"received_through"`
	Suppressed      bool            `json:"suppressed"`
	Events          remoteLogEvents `json:"events"`
}

type remoteLogEvents struct {
	Tail []RemoteLogEvent `json:"tail"`
	Gaps []RemoteLogEvent `json:"gaps"`
}

func (r *Repository) decodeRemoteLogBuffer(
	value sql.NullString,
	lastSequence int64,
) (*remoteLogBuffer, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	plaintext, err := r.decryptValue(value)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Version *int            `json:"version"`
		Events  json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal([]byte(plaintext.String), &envelope); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(plaintext.String), &fields); err != nil {
		return nil, err
	}
	if _, exists := fields["version"]; exists && envelope.Version == nil {
		return nil, ErrInvalidRemoteLog
	}
	if envelope.Version == nil {
		return decodeLegacyRemoteLogBuffer(envelope.Events, lastSequence)
	}
	var state remoteLogBuffer
	if err := json.Unmarshal([]byte(plaintext.String), &state); err != nil {
		return nil, err
	}
	if err := validateRemoteLogFields(fields); err != nil {
		return nil, err
	}
	if err := validateRemoteLogState(&state, lastSequence); err != nil {
		return nil, err
	}
	return &state, nil
}

func validateRemoteLogFields(fields map[string]json.RawMessage) error {
	for _, name := range []string{"version", "received_through", "suppressed", "events"} {
		if len(fields[name]) == 0 || string(fields[name]) == "null" {
			return ErrInvalidRemoteLog
		}
	}
	var eventFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["events"], &eventFields); err != nil {
		return err
	}
	if len(eventFields["tail"]) == 0 || len(eventFields["gaps"]) == 0 {
		return ErrInvalidRemoteLog
	}
	return nil
}

func decodeLegacyRemoteLogBuffer(
	raw json.RawMessage,
	lastSequence int64,
) (*remoteLogBuffer, error) {
	var events []RemoteLogEvent
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ErrInvalidRemoteLog
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		return nil, err
	}
	state := &remoteLogBuffer{Version: 2, ReceivedThrough: lastSequence}
	contiguous, gaps, cursor, err := admitRemoteLogEvents(state, events)
	if err != nil {
		return nil, err
	}
	state.ReceivedThrough = cursor
	state.Events = remoteLogEvents{Tail: contiguous, Gaps: gaps}
	return state, nil
}

func (r *Repository) encodeRemoteLogBuffer(
	state *remoteLogBuffer,
) (sql.NullString, error) {
	if state == nil {
		return sql.NullString{}, nil
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return sql.NullString{}, err
	}
	return r.encryptValue(sql.NullString{String: string(raw), Valid: true})
}

// Admission depends only on durable receipt progress and raw input. The tail
// never participates in a rejection: its capacity is recovered by suppression.
func admitRemoteLogEvents(
	state *remoteLogBuffer,
	incoming []RemoteLogEvent,
) ([]RemoteLogEvent, []RemoteLogEvent, int64, error) {
	if !remoteLogEventsFit(incoming) {
		return nil, nil, 0, ErrRemoteLifecycleConflict
	}
	bySequence := make(map[int64]RemoteLogEvent)
	for _, event := range state.Events.Gaps {
		bySequence[event.Sequence] = event
	}
	for _, event := range incoming {
		if event.Sequence < 0 {
			return nil, nil, 0, ErrInvalidRemoteLog
		}
		if event.Sequence <= state.ReceivedThrough {
			continue
		}
		if _, exists := bySequence[event.Sequence]; !exists {
			bySequence[event.Sequence] = event
		}
	}
	merged := make([]RemoteLogEvent, 0, len(bySequence))
	for _, event := range bySequence {
		merged = append(merged, event)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Sequence < merged[j].Sequence
	})
	cursor := state.ReceivedThrough
	count := contiguousEventCount(merged, cursor)
	if count != 0 {
		cursor = merged[count-1].Sequence
	}
	gaps := merged[count:]
	if err := validateRemoteLogGaps(gaps, cursor); err != nil {
		return nil, nil, 0, err
	}
	return merged[:count], gaps, cursor, nil
}

func contiguousEventCount(events []RemoteLogEvent, cursor int64) int {
	count := 0
	for _, event := range events {
		if cursor == math.MaxInt64 {
			break
		}
		expected := cursor + 1
		if cursor == -1 && event.Sequence != 0 {
			expected = 1
		}
		if event.Sequence != expected {
			break
		}
		cursor = event.Sequence
		count++
	}
	return count
}

func validateRemoteLogGaps(gaps []RemoteLogEvent, cursor int64) error {
	if !remoteLogEventsFit(gaps) {
		return ErrRemoteLifecycleConflict
	}
	if len(gaps) != 0 {
		expected := cursor + 1 // A gap cannot follow MaxInt64.
		if cursor == -1 {
			expected = 1
		}
		if gaps[len(gaps)-1].Sequence-expected >= agentproto.MaxLogEvents {
			return ErrRemoteLifecycleConflict
		}
	}
	return nil
}

func prepareRemoteLogEvents(
	pending *remoteLogBuffer,
	incoming []RemoteLogEvent,
	lastSequence int64,
	scrubber *logscrub.Scrubber,
	flush bool,
) ([]RemoteLogEvent, *remoteLogBuffer, error) {
	if pending == nil {
		pending = &remoteLogBuffer{Version: 2, ReceivedThrough: lastSequence}
	}
	contiguous, gaps, cursor, err := admitRemoteLogEvents(pending, incoming)
	if err != nil {
		return nil, nil, err
	}
	merged := append(
		append([]RemoteLogEvent(nil), pending.Events.Tail...),
		contiguous...)
	if flush {
		merged = append(merged, gaps...)
		gaps = nil
	}
	readyCount := len(merged)
	if !flush && !pending.Suppressed {
		parts := make([]string, len(merged))
		for index, event := range merged {
			parts[index] = event.Line
		}
		readyCount = readyEventCount(
			merged,
			scrubber.PendingBytes(strings.Join(parts, "")),
		)
	}
	state := &remoteLogBuffer{
		Version: 2, ReceivedThrough: cursor, Suppressed: pending.Suppressed,
		Events: remoteLogEvents{Tail: merged[readyCount:], Gaps: gaps},
	}
	if !remoteLogEventsFit(
		append(append([]RemoteLogEvent(nil), state.Events.Tail...), gaps...),
	) {
		state.Suppressed = true
		readyCount = len(merged)
		state.Events.Tail = nil
	}
	ready := merged[:readyCount]
	if state.Suppressed {
		for index := range ready {
			ready[index].Line = "[REDACTED]"
		}
	} else {
		scrubRemoteLogEvents(ready, scrubber)
	}
	if flush {
		return ready, nil, nil
	}
	return ready, state, nil
}

func scrubRemoteLogEvents(
	events []RemoteLogEvent,
	scrubber *logscrub.Scrubber,
) {
	parts := make([]string, len(events))
	for index, event := range events {
		parts[index] = event.Line
	}
	parts = scrubber.ScrubParts(parts)
	for index := range events {
		events[index].Line = scrubber.Scrub(parts[index])
	}
}

func remoteLogEventsFit(events []RemoteLogEvent) bool {
	if len(events) > agentproto.MaxLogEvents {
		return false
	}
	remaining := maxRemoteLogBufferBytes
	for _, event := range events {
		if len(event.Line) > remaining {
			return false
		}
		remaining -= len(event.Line)
	}
	return true
}

func validateRemoteLogState(state *remoteLogBuffer, published int64) error {
	if state.Version != 2 || state.ReceivedThrough < -1 ||
		state.ReceivedThrough < published ||
		(state.Suppressed && len(state.Events.Tail) != 0) {
		return ErrInvalidRemoteLog
	}
	all := append(
		append([]RemoteLogEvent(nil), state.Events.Tail...),
		state.Events.Gaps...)
	if !remoteLogEventsFit(all) {
		return ErrInvalidRemoteLog
	}
	if !validRemoteLogTail(state, published) {
		return ErrInvalidRemoteLog
	}
	previous := state.ReceivedThrough
	for _, event := range state.Events.Gaps {
		if event.Sequence < 0 || event.Sequence <= previous {
			return ErrInvalidRemoteLog
		}
		previous = event.Sequence
	}
	contiguous, _, _, err := admitRemoteLogEvents(state, nil)
	if err != nil || len(contiguous) != 0 {
		return ErrInvalidRemoteLog
	}
	return nil
}

func validRemoteLogTail(state *remoteLogBuffer, published int64) bool {
	previous := published
	for _, event := range state.Events.Tail {
		if event.Sequence < 0 || event.Sequence <= previous ||
			event.Sequence > state.ReceivedThrough ||
			(event.Sequence-previous != 1 && (previous != -1 || event.Sequence != 1)) {
			return false
		}
		previous = event.Sequence
	}
	return previous == state.ReceivedThrough
}

func readyEventCount(events []RemoteLogEvent, holdBytes int) int {
	totalBytes := 0
	for _, event := range events {
		totalBytes += len(event.Line)
	}
	safeBytes := totalBytes - holdBytes
	if safeBytes <= 0 {
		return 0
	}
	ready, consumed := 0, 0
	for ready < len(events) {
		consumed += len(events[ready].Line)
		if consumed > safeBytes {
			break
		}
		ready++
	}
	return ready
}

func remoteLogTerminal(state string) bool {
	switch state {
	case "succeeded",
		"failed",
		"cancelled",
		"cleanup_unconfirmed",
		"lost",
		"cancel_unconfirmed":
		return true
	}
	return false
}
