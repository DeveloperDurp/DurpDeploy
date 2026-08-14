package mfa

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"image/png"
	"io"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
	"github.com/pquerna/otp/totp"
)

const (
	totpSeedBytes     = 20
	totpPeriodSeconds = 30
)

var (
	ErrInvalidTOTP = errors.New("invalid TOTP")
	ErrTOTPReplay  = errors.New("replayed TOTP")
)

// TOTP generates and verifies only SHA-1, six-digit, 30-second TOTP values.
type TOTP struct {
	now    func() time.Time
	random io.Reader
}

// TOTPEnrollment is a one-time TOTP setup value. Callers must not persist Seed
// or include it in logs.
type TOTPEnrollment struct {
	Seed string
	URI  string
	PNG  []byte
}

// NewTOTP constructs a TOTP helper. Nil values use crypto/rand.Reader and
// time.Now; tests can inject deterministic seams without mutable global state.
func NewTOTP(clock func() time.Time, random io.Reader) TOTP {
	if clock == nil {
		clock = time.Now
	}
	if random == nil {
		random = rand.Reader
	}
	return TOTP{now: clock, random: random}
}

// Enroll generates a 20-byte seed and fixed-configuration provisioning data.
func (t TOTP) Enroll(issuer, accountName string) (TOTPEnrollment, error) {
	seed, err := t.generateSeed()
	if err != nil {
		return TOTPEnrollment{}, err
	}
	seedBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).
		DecodeString(seed)
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf(
			"decode generated TOTP seed: %w",
			err,
		)
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      totpPeriodSeconds,
		Secret:      seedBytes,
		SecretSize:  totpSeedBytes,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf(
			"generate TOTP provisioning data: %w",
			err,
		)
	}
	image, err := key.Image(200, 200)
	if err != nil {
		return TOTPEnrollment{}, fmt.Errorf(
			"render TOTP provisioning image: %w",
			err,
		)
	}
	var encodedPNG bytes.Buffer
	if err := png.Encode(&encodedPNG, image); err != nil {
		return TOTPEnrollment{}, fmt.Errorf(
			"encode TOTP provisioning image: %w",
			err,
		)
	}
	return TOTPEnrollment{
		Seed: seed,
		URI:  key.URL(),
		PNG:  encodedPNG.Bytes(),
	}, nil
}

// Verify returns the exact accepted 30-second step for transactional replay
// protection. A later storage layer must atomically reject steps not newer than
// its persisted last accepted step.
func (t TOTP) Verify(code, seed string, lastAcceptedStep int64) (int64, error) {
	if !validTOTPCode(code) {
		return 0, ErrInvalidTOTP
	}
	currentStep := t.now().Unix() / totpPeriodSeconds
	matched := false
	for _, offset := range []int64{-1, 0, 1} {
		step := currentStep + offset
		if step < 0 {
			continue
		}
		valid, err := hotp.ValidateCustom(
			code,
			uint64(step),
			seed,
			hotp.ValidateOpts{
				Digits:    otp.DigitsSix,
				Algorithm: otp.AlgorithmSHA1,
			},
		)
		if err != nil {
			return 0, ErrInvalidTOTP
		}
		if !valid {
			continue
		}
		matched = true
		if step <= lastAcceptedStep {
			continue
		}
		return step, nil
	}
	if matched {
		return 0, ErrTOTPReplay
	}
	return 0, ErrInvalidTOTP
}

// GenerateTOTPCode produces a fixed-configuration code for a supplied time.
func GenerateTOTPCode(seed string, at time.Time) (string, error) {
	code, err := totp.GenerateCodeCustom(seed, at, totp.ValidateOpts{
		Period:    totpPeriodSeconds,
		Skew:      0,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", ErrInvalidTOTP
	}
	return code, nil
}

func (t TOTP) generateSeed() (string, error) {
	seed := make([]byte, totpSeedBytes)
	if _, err := io.ReadFull(t.random, seed); err != nil {
		return "", fmt.Errorf("generate TOTP seed: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).
			EncodeToString(seed),
		nil
}

func validTOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
