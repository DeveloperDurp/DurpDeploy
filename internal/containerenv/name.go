package containerenv

import (
	"errors"
	"regexp"
	"strings"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var ErrIdentifier = errors.New("variable name is not an identifier")

var ErrReserved = errors.New(
	"variable name is reserved for the container runner",
)

// ValidateName keeps agent variable names compatible while preventing local
// selections from changing the container and SSH clients' own environment.
func ValidateName(name string, local bool) error {
	if !identifier.MatchString(name) {
		return ErrIdentifier
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
