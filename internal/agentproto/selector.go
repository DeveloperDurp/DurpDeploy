package agentproto

import (
	"sort"
	"strings"
	"unicode/utf8"
)

type SelectorKey string
type SelectorValue string

type SelectorEntry struct {
	Key   SelectorKey
	Value SelectorValue
}

type Selector struct {
	entries []SelectorEntry
}

func ParseSelector(raw string) (Selector, error) {
	if strings.TrimSpace(raw) == "" {
		return Selector{}, nil
	}

	pairs := strings.Split(raw, ",")
	if len(pairs) > 32 {
		return Selector{}, protocolError(
			"selector",
			ReasonLimit,
			ErrSelectorLimit,
		)
	}

	entries := make([]SelectorEntry, 0, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			return Selector{}, protocolError(
				"selector",
				ReasonInvalid,
				ErrInvalidSelector,
			)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !validSelectorKey(key) || !validSelectorValue(value) {
			return Selector{}, protocolError(
				"selector",
				ReasonInvalid,
				ErrInvalidSelector,
			)
		}
		selectorKey := SelectorKey(key)
		if containsSelectorKey(entries, selectorKey) {
			return Selector{}, protocolError(
				"selector",
				ReasonDuplicate,
				ErrDuplicateSelectorKey,
			)
		}
		entries = append(entries, SelectorEntry{
			Key:   selectorKey,
			Value: SelectorValue(value),
		})
	}

	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Key < entries[right].Key
	})
	return Selector{entries: entries}, nil
}

func (s Selector) Entries() []SelectorEntry {
	return append([]SelectorEntry(nil), s.entries...)
}

func (s Selector) String() string {
	pairs := make([]string, len(s.entries))
	for index, entry := range s.entries {
		pairs[index] = string(entry.Key) + "=" + string(entry.Value)
	}
	return strings.Join(pairs, ",")
}

func validSelectorKey(value string) bool {
	if len(value) == 0 || len(value) > 32 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '.' && character != '-' {
			return false
		}
	}
	return true
}

func validSelectorValue(value string) bool {
	return len(value) > 0 && len(value) <= 64 && utf8.ValidString(value)
}

func containsSelectorKey(entries []SelectorEntry, key SelectorKey) bool {
	for _, entry := range entries {
		if entry.Key == key {
			return true
		}
	}
	return false
}
