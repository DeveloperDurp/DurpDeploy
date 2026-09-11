package handler_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestAlpineCloakRule(t *testing.T) {
	// Given
	cloakRule := regexp.MustCompile(
		`(?s)\[x-cloak\]\s*\{\s*display\s*:\s*none\s*!important\s*;?\s*\}`,
	)
	stylesheets := []string{
		filepath.Join("..", "..", "static", "css", "input.css"),
		filepath.Join("..", "..", "static", "css", "tailwind.min.css"),
	}

	for _, stylesheet := range stylesheets {
		t.Run(filepath.Base(stylesheet), func(t *testing.T) {
			// When
			contents, err := os.ReadFile(stylesheet)
			if err != nil {
				t.Fatalf("read stylesheet %q: %v", stylesheet, err)
			}

			// Then
			if !cloakRule.Match(contents) {
				t.Fatalf("stylesheet %q has no effective [x-cloak] rule", stylesheet)
			}
		})
	}
}
