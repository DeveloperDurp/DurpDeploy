package secret

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestConfiguredKeyPrecedenceAndFallback(t *testing.T) {
	configured := bytes.Repeat([]byte{1}, 32)
	fileKey := bytes.Repeat([]byte{2}, 32)
	lookup := func(string) (string, bool) {
		return base64.StdEncoding.EncodeToString(configured), true
	}
	actual, err := loadKey(lookup, func() ([]byte, error) {
		t.Fatal("configured key must precede the legacy file")
		return nil, nil
	})
	if err != nil || !bytes.Equal(actual, configured) {
		t.Fatal("configured key was not selected")
	}
	for _, set := range []bool{false, true} {
		actual, err = loadKey(
			func(string) (string, bool) { return "", set },
			func() ([]byte, error) {
				return []byte(base64.StdEncoding.EncodeToString(fileKey)), nil
			},
		)
		if err != nil || !bytes.Equal(actual, fileKey) {
			t.Fatal(
				"empty or absent configured key lost the legacy file fallback",
			)
		}
	}
	_, err = loadKey(
		func(string) (string, bool) { return "private-marker", true },
		func() ([]byte, error) {
			t.Fatal("invalid explicit key must not fall back to another key")
			return nil, nil
		},
	)
	if err == nil || strings.Contains(err.Error(), "private-marker") {
		t.Fatal("invalid key was accepted or exposed")
	}
	for _, fileErr := range []error{os.ErrNotExist, os.ErrPermission} {
		_, err := loadKey(
			func(string) (string, bool) { return "", false },
			func() ([]byte, error) { return nil, fileErr },
		)
		if err == nil {
			t.Fatal("missing or unreadable fallback key must fail")
		}
	}
	_, err = loadKey(
		func(string) (string, bool) { return "", false },
		func() ([]byte, error) { return []byte("invalid"), nil },
	)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid fallback key must fail decoding")
	}
}
