package containerenv

import (
	"errors"
	"testing"
)

func TestValidateNameRejectsClientConfigEnvironment(t *testing.T) {
	for _, name := range []string{"PATH", "HOME", "SSH_AUTH_SOCK", "XDG_DATA_HOME"} {
		if err := ValidateName(name, true); !errors.Is(err, ErrReserved) {
			t.Fatalf("%s: expected reserved name, got %v", name, err)
		}
		if err := ValidateName(name, false); err != nil {
			t.Fatalf("agent variable %s rejected: %v", name, err)
		}
	}
}
