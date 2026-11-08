package repository

import (
	"errors"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var ErrProjectName = errors.New(
	"project name must contain 1 to 255 UTF-16 code units",
)

// validateProjectName matches SQL Server's NVARCHAR(255) storage limit.
func validateProjectName(name string) error {
	if strings.TrimSpace(name) == "" || !utf8.ValidString(name) {
		return ErrProjectName
	}
	units := 0
	for _, character := range name {
		units += utf16.RuneLen(character)
		if units > 255 {
			return ErrProjectName
		}
	}
	return nil
}
