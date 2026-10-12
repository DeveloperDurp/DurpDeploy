package mfa

import (
	"errors"
	"strings"
	"unicode/utf8"
)

var ErrPasskeyName = errors.New(
	"Enter a passkey name of 1 to 255 characters. Some symbols count as two characters.",
)

// NormalizePasskeyName matches NVARCHAR(255) and HTML maxlength semantics.
func NormalizePasskeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) {
		return "", ErrPasskeyName
	}
	units := 0
	for _, character := range name {
		units++
		if character > 0xffff {
			units++
		}
		if units > 255 {
			return "", ErrPasskeyName
		}
	}
	return name, nil
}
