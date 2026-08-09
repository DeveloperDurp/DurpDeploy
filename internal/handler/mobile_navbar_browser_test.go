//go:build mobilebrowser

package handler_test

import (
	"path/filepath"
	"testing"
)

func TestMobileBrowser_Navbar_closes_and_preserves_desktop_controls(
	t *testing.T,
) {
	// Given
	root := repositoryRoot(t)
	t.Setenv("MOBILE_TARGETS", "navbar")
	t.Setenv("MOBILE_STRICT", "1")
	t.Setenv("MOBILE_EVIDENCE_FILE", "mobile-navbar-receipt.json")
	t.Setenv(
		"MOBILE_DURABLE_EVIDENCE_DIR",
		filepath.Join(root, ".omo", "evidence", "mobile-navbar"),
	)

	// When / Then
	TestMobileBrowserReadability(t)
}
