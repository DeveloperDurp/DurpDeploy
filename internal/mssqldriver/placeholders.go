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
			if i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9' {
				end := i + 1
				for end < len(query) && query[end] >= '0' && query[end] <= '9' {
					end++
				}
				explicit, err := strconv.Atoi(query[i+1 : end])
				if err == nil {
					if explicit > ordinal {
						ordinal = explicit
					}
					result.WriteString("@p")
					result.WriteString(strconv.Itoa(explicit))
					i = end
					continue
				}
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
