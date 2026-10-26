package containerenv

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var ErrIdentifier = errors.New("variable name is not an identifier")

var ErrEnvironment = errors.New("invalid execution environment")

var ErrReserved = errors.New(
	"variable name is reserved for the container runner",
)

const StageVariable = "DURPDEPLOY_STAGE_DIR"
const ApprovedVariable = "DURPDEPLOY_APPROVED_DIR"

// ValidateName keeps agent variable names compatible while preventing local
// selections from changing the container and SSH clients' own environment.
func ValidateName(name string, local bool) error {
	if err := ValidateIdentifier(name); err != nil {
		return err
	}
	if name == "ARTIFACT_PATH" || name == StageVariable ||
		name == ApprovedVariable {
		return ErrReserved
	}
	if local && (name == "PATH" || name == "HOME" || name == "TERM" ||
		name == "TMPDIR" ||
		name == "REGISTRY_AUTH_FILE" ||
		strings.HasPrefix(name, "XDG_") ||
		strings.HasPrefix(name, "DOCKER_") ||
		strings.HasPrefix(name, "PODMAN_") ||
		strings.HasPrefix(name, "CONTAINER_") ||
		strings.HasPrefix(name, "CONTAINERS_") ||
		strings.HasPrefix(name, "SSH_")) {
		return ErrReserved
	}
	return nil
}

// ValidateIdentifier does not apply execution-target reserved-name rules.
func ValidateIdentifier(name string) error {
	if len(name) > 255 || !identifier.MatchString(name) {
		return ErrIdentifier
	}
	return nil
}

// ValidateEntry checks only an effective environment entry, without its value
// appearing in errors. Resolve overrides and selections before calling it.
func ValidateEntry(name, value string) error {
	if err := ValidateIdentifier(name); err != nil {
		return fmt.Errorf("%w: %w", ErrEnvironment, err)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%w: value contains NUL", ErrEnvironment)
	}
	return nil
}
