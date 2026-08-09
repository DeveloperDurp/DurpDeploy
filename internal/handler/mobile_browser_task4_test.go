//go:build mobilebrowser

package handler_test

import "testing"

func TestMobileBrowserTask4Readability(t *testing.T) {
	// Given
	t.Setenv("MOBILE_TARGETS", "templates,template-history,projects,audit")
	t.Setenv("MOBILE_STRICT", "1")

	// When / Then
	TestMobileBrowserReadability(t)
}
