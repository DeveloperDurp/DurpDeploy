package mssqldriver

import (
	"regexp"
	"strings"
)

var (
	limitRe = regexp.MustCompile(
		`(?is)\s+LIMIT\s+(@p\d+|\d+)(?:\s+OFFSET\s+(@p\d+|\d+))?(\s*(?:\)\s*)?;?\s*)$`,
	)
	nestedLimitRe = regexp.MustCompile(
		`(?is)\s+LIMIT\s+(@p\d+|\d+)(\s*\))`,
	)
)

func rewriteLimit(query string) string {
	match := limitRe.FindStringSubmatchIndex(query)
	if match == nil {
		return rewriteNestedLimit(query)
	}
	limit := query[match[2]:match[3]]
	offset := ""
	if match[4] >= 0 {
		offset = query[match[4]:match[5]]
	}
	suffix := query[match[6]:match[7]]
	if offset != "" {
		limitStart := strings.Index(
			strings.ToUpper(query[match[0]:match[1]]),
			"LIMIT",
		)
		separator := query[match[0] : match[0]+limitStart]
		return query[:match[0]] + separator + "OFFSET " + offset + " ROWS FETCH NEXT " + limit + " ROWS ONLY" + suffix
	}
	selectIndex := keywordIndexOutsideQuotes(query[:match[0]], "SELECT")
	if selectIndex < 0 {
		return query
	}
	insertAt := selectIndex + len("SELECT")
	return query[:insertAt] + " TOP (" + limit + ")" + query[insertAt:match[0]] + suffix
}

func rewriteNestedLimit(query string) string {
	match := nestedLimitRe.FindStringSubmatchIndex(query)
	if match == nil {
		return query
	}
	close := match[3]
	for close < len(query) && strings.ContainsRune(" \t\r\n", rune(query[close])) {
		close++
	}
	open := matchingOpenParen(query, close)
	if open < 0 {
		return query
	}
	selectIndex := keywordIndexOutsideQuotes(query[open:match[0]], "SELECT")
	if selectIndex < 0 {
		return query
	}
	selectIndex += open
	limit := query[match[2]:match[3]]
	insertAt := selectIndex + len("SELECT")
	return query[:insertAt] + " TOP (" + limit + ")" + query[insertAt:match[0]] + query[match[3]:]
}

func matchingOpenParen(query string, close int) int {
	stack := make([]int, 0, 4)
	for index := 0; index <= close; index++ {
		if quoteEnd, ok := quotedEnd(query, index); ok {
			index = quoteEnd - 1
			continue
		}
		switch query[index] {
		case '(':
			stack = append(stack, index)
		case ')':
			if len(stack) == 0 {
				return -1
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if index == close {
				return open
			}
		}
	}
	return -1
}
