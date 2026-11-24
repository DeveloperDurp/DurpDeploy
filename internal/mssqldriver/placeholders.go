package mssqldriver

import (
	"strconv"
	"strings"
)

func rewritePlaceholders(query string) string {
	var result strings.Builder
	result.Grow(len(query) + 8)
	ordinal := 0
	for i := 0; i < len(query); {
		if quoteEnd, ok := quotedEnd(query, i); ok {
			result.WriteString(query[i:quoteEnd])
			i = quoteEnd
			continue
		}
		if query[i] == '?' {
			if index, end, ok := numberedPlaceholder(query, i+1); ok {
				ordinal = max(ordinal, index)
				result.WriteString("@p")
				result.WriteString(strconv.Itoa(index))
				i = end
				continue
			}
			ordinal++
			result.WriteString("@p")
			result.WriteString(strconv.Itoa(ordinal))
			i++
			continue
		}
		result.WriteByte(query[i])
		i++
	}
	return result.String()
}

func numberedPlaceholder(query string, start int) (int, int, bool) {
	end := start
	for end < len(query) && query[end] >= '0' && query[end] <= '9' {
		end++
	}
	if end == start {
		return 0, start, false
	}
	index, err := strconv.Atoi(query[start:end])
	return index, end, err == nil
}
