package containerenv

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateEntryRejectsMalformedEnvironment(t *testing.T) {
	for _, name := range []string{"", "BAD\x00NAME", "BAD=NAME", "bad name", "BAD\nNAME", "2NAME", "NÁME", strings.Repeat("A", 256)} {
		if err := ValidateEntry(name, "private-value"); !errors.Is(
			err,
			ErrEnvironment,
		) ||
			strings.Contains(err.Error(), "private-value") {
			t.Fatalf("name %q error=%v", name, err)
		}
	}
	if err := ValidateEntry("VALID", "private\x00value"); !errors.Is(
		err,
		ErrEnvironment,
	) ||
		strings.Contains(err.Error(), "private") {
		t.Fatalf("NUL value: %v", err)
	}
	for _, name := range []string{"Mixed_2", "_EMPTY", "HOME", "ARTIFACT_PATH", StageVariable, strings.Repeat("A", 255)} {
		for _, value := range []string{"", "first\nsecond\rthird"} {
			if err := ValidateEntry(name, value); err != nil {
				t.Fatalf(
					"entry check applied reserved policy to %q: %v",
					name,
					err,
				)
			}
		}
	}
}
