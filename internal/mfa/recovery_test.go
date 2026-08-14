package mfa

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRecovery_Generate_createsTenUniquePrintableCodes(t *testing.T) {
	// Given
	randomBytes := make([]byte, recoveryCodeBytes*recoveryCodeCount)
	for index := range randomBytes {
		randomBytes[index] = byte(index)
	}
	random := bytes.NewReader(randomBytes)

	// When
	codes, err := GenerateRecoveryCodes(random)

	// Then
	if err != nil {
		t.Fatalf("generate recovery codes: %v", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatal("recovery code count mismatch")
	}
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if len(code) != 39 || strings.Count(code, "-") != 7 {
			t.Fatal("recovery code format mismatch")
		}
		if _, exists := seen[code]; exists {
			t.Fatal("generated recovery code was duplicated")
		}
		seen[code] = struct{}{}
	}
}

func TestRecovery_NormalizeHashAndCompare_acceptEquivalentFormatting(
	t *testing.T,
) {
	// Given
	formatted := formatRecoveryCode(
		bytes.Repeat([]byte{0xab}, recoveryCodeBytes),
	)

	// When
	normalized, err := NormalizeRecoveryCode(
		" " + strings.ToLower(formatted) + " ",
	)
	if err != nil {
		t.Fatalf("normalize recovery code: %v", err)
	}
	hash, err := HashRecoveryCode(formatted)
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	matches, err := RecoveryCodeMatches(hash, normalized)

	// Then
	if err != nil {
		t.Fatalf("compare recovery code: %v", err)
	}
	if !matches {
		t.Fatal("equivalent recovery code did not match its hash")
	}
}

func TestRecovery_Normalize_rejectsMalformedInput(t *testing.T) {
	for _, code := range []string{"", "abcd", "zzzz-0123-abcd-0123-abcd-0123-abcd-0123"} {
		t.Run("malformed code", func(t *testing.T) {
			// When
			_, err := NormalizeRecoveryCode(code)

			// Then
			if !errors.Is(err, ErrInvalidRecoveryCode) {
				t.Fatal("malformed recovery code was not rejected")
			}
		})
	}
}

func TestRecovery_Generate_returnsErrorWhenRandomSourceFails(t *testing.T) {
	// Given
	random := failingReader{}

	// When
	_, err := GenerateRecoveryCodes(random)

	// Then
	if !errors.Is(err, ErrRecoveryRandom) {
		t.Fatal("random-source failure was not returned")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("random source failed")
}
