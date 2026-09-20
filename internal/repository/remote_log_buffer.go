package repository

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"durpdeploy/internal/logscrub"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

const maxRemoteLogBufferBytes = agentproto.MaxLogBatchBytes

type remoteLogBuffer struct {
	Events []RemoteLogEvent `json:"events"`
}

func (r *Repository) decodeRemoteLogBuffer(
	value sql.NullString,
) ([]RemoteLogEvent, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	plaintext, err := r.decryptValue(value)
	if err != nil {
		return nil, err
	}
	var buffer remoteLogBuffer
	if err := json.Unmarshal([]byte(plaintext.String), &buffer); err != nil {
		return nil, err
	}
	return buffer.Events, nil
}

func (r *Repository) encodeRemoteLogBuffer(
	events []RemoteLogEvent,
) (sql.NullString, error) {
	if len(events) == 0 {
		return sql.NullString{}, nil
	}
	raw, err := json.Marshal(remoteLogBuffer{Events: events})
	if err != nil {
		return sql.NullString{}, err
	}
	return r.encryptValue(sql.NullString{
		String: string(raw),
		Valid:  true,
	})
}

func prepareRemoteLogEvents(
	pending []RemoteLogEvent,
	incoming []RemoteLogEvent,
	lastSequence int64,
	scrubber *logscrub.Scrubber,
	flush bool,
) ([]RemoteLogEvent, []RemoteLogEvent, error) {
	bySequence := make(map[int64]RemoteLogEvent, len(pending)+len(incoming))
	for _, event := range pending {
		if event.Sequence < 0 {
			return nil, nil, ErrInvalidRemoteLog
		}
		bySequence[event.Sequence] = event
	}
	for _, event := range incoming {
		if event.Sequence < 0 {
			return nil, nil, ErrInvalidRemoteLog
		}
		if event.Sequence > lastSequence {
			if _, exists := bySequence[event.Sequence]; !exists {
				bySequence[event.Sequence] = event
			}
		}
	}
	merged := make([]RemoteLogEvent, 0, len(bySequence))
	for _, event := range bySequence {
		merged = append(merged, event)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Sequence < merged[j].Sequence
	})

	readyCount := len(merged)
	if !flush {
		expected := lastSequence + 1
		if lastSequence < 0 && len(merged) != 0 && merged[0].Sequence != 0 {
			expected = 1
		}
		contiguous := contiguousEventCount(merged, expected)
		parts := make([]string, contiguous)
		for index := range contiguous {
			parts[index] = merged[index].Line
		}
		pendingBytes := scrubber.PendingBytes(
			strings.Join(parts, ""),
		)
		readyCount = readyEventCount(
			merged[:contiguous], pendingBytes,
		)
	}
	ready := append([]RemoteLogEvent(nil), merged[:readyCount]...)
	parts := make([]string, len(ready))
	for index := range ready {
		parts[index] = ready[index].Line
	}
	parts = scrubber.ScrubParts(parts)
	for index := range ready {
		ready[index].Line = scrubber.Scrub(parts[index])
	}
	remaining := append([]RemoteLogEvent(nil), merged[readyCount:]...)
	if err := validateRemoteLogBuffer(remaining, lastSequence, ready); err != nil {
		return nil, nil, err
	}
	return ready, remaining, nil
}

func contiguousEventCount(
	events []RemoteLogEvent,
	expectedSequence int64,
) int {
	contiguous := 0
	for _, event := range events {
		if event.Sequence != expectedSequence {
			break
		}
		contiguous++
		expectedSequence++
	}
	return contiguous
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
	ready := 0
	consumed := 0
	for ready < len(events) {
		consumed += len(events[ready].Line)
		if consumed > safeBytes {
			break
		}
		ready++
	}
	return ready
}

func validateRemoteLogBuffer(
	events []RemoteLogEvent,
	lastSequence int64,
	ready []RemoteLogEvent,
) error {
	if len(events) > agentproto.MaxLogEvents {
		return ErrRemoteLifecycleConflict
	}
	totalBytes := 0
	for _, event := range events {
		totalBytes += len(event.Line)
	}
	if totalBytes > maxRemoteLogBufferBytes {
		return ErrRemoteLifecycleConflict
	}
	expected := lastSequence + 1
	if len(ready) != 0 {
		expected = ready[len(ready)-1].Sequence + 1
	} else if lastSequence < 0 && len(events) != 0 && events[0].Sequence != 0 {
		expected = 1
	}
	if len(events) != 0 &&
		events[len(events)-1].Sequence-expected >= agentproto.MaxLogEvents {
		return ErrRemoteLifecycleConflict
	}
	return nil
}
