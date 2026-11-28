package notify

import (
	"errors"
	"net/mail"
	"strings"
	"unicode"
)

var ErrInvalidRecipients = errors.New(
	"notification recipients must be comma-separated email addresses without control characters",
)

// ValidateRecipients matches the comma-separated delivery representation.
func ValidateRecipients(raw string) error {
	if strings.ContainsFunc(raw, unicode.IsControl) {
		return ErrInvalidRecipients
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		address, err := mail.ParseAddress(entry)
		if err != nil || address.Name != "" || decoratedAddress(entry) {
			return ErrInvalidRecipients
		}
	}
	return nil
}

// ParseAddress decodes quoted local parts. Inspect only decorations outside
// those quotes instead of comparing the decoded address with the input.
func decoratedAddress(entry string) bool {
	quoted, escaped := false, false
	for _, char := range entry {
		if escaped {
			escaped = false
			continue
		}
		if quoted && char == '\\' {
			escaped = true
		} else if char == '"' {
			quoted = !quoted
		} else if !quoted && (strings.ContainsRune("<>();", char) ||
			unicode.IsSpace(char)) {
			return true
		}
	}
	return false
}
