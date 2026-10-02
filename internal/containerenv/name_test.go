package containerenv

import (
	"errors"
	"testing"
)

func TestValidateNameRejectsClientConfigEnvironment(t *testing.T) {
	for _, name := range []string{
		"PATH", "HOME", "TERM", "SSH_AUTH_SOCK", "XDG_DATA_HOME", "DOCKER_HOST",
	} {
		if err := ValidateName(name, true); !errors.Is(err, ErrReserved) {
			t.Fatalf("%s: expected reserved name, got %v", name, err)
		}
		if err := ValidateName(name, false); err != nil {
			t.Fatalf("agent variable %s rejected: %v", name, err)
		}
	}
}

func TestValidateNameRejectsStagingVariable(t *testing.T) {
	// Given: the staging path is supplied by the runner for local steps.
	for _, local := range []bool{true, false} {
		// When: a user selects the reserved name.
		err := ValidateName(StageVariable, local)
		// Then: neither local nor agent selections can override the contract.
		if !errors.Is(err, ErrReserved) {
			t.Fatalf("local=%v: expected reserved name, got %v", local, err)
		}
	}
}
