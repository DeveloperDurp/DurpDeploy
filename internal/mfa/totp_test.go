package mfa

import (
	"bytes"
	"errors"
	"image/png"
	"strings"
	"testing"
	"time"
)

func TestTOTP_GenerateCode_matchesRFC6238SHA1Vectors(t *testing.T) {
	tests := []struct {
		at   int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	const seed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

	for _, test := range tests {
		t.Run("RFC vector", func(t *testing.T) {
			// Given
			at := time.Unix(test.at, 0).UTC()

			// When
			code, err := GenerateTOTPCode(seed, at)

			// Then
			if err != nil {
				t.Fatalf("generate RFC TOTP code: %v", err)
			}
			if code != test.want {
				t.Fatal("RFC TOTP code mismatch")
			}
		})
	}
}

func TestTOTP_Enroll_createsProvisioningDataAndSurfacesRandomFailure(
	t *testing.T,
) {
	// Given
	randomBytes := make([]byte, totpSeedBytes)
	for index := range randomBytes {
		randomBytes[index] = byte(index)
	}
	generator := NewTOTP(nil, bytes.NewReader(randomBytes))

	// When
	enrollment, err := generator.Enroll("DurpDeploy", "user@example.com")

	// Then
	if err != nil {
		t.Fatalf("enroll TOTP: %v", err)
	}
	if !strings.Contains(enrollment.URI, "secret="+enrollment.Seed) {
		t.Fatal("provisioning URI did not contain generated seed")
	}
	if _, err := png.Decode(bytes.NewReader(enrollment.PNG)); err != nil {
		t.Fatalf("decode provisioning PNG: %v", err)
	}

	// When
	_, err = NewTOTP(
		nil,
		failingReader{},
	).Enroll("DurpDeploy", "user@example.com")

	// Then
	if err == nil {
		t.Fatal("TOTP random-source failure was not returned")
	}
}

func TestTOTP_Verify_returnsCurrentAndAdjacentSteps(t *testing.T) {
	// Given
	const seed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	clock := func() time.Time { return time.Unix(1111111111, 0).UTC() }
	verifier := NewTOTP(clock, nil)
	currentStep := clock().Unix() / 30

	for _, step := range []int64{currentStep - 1, currentStep, currentStep + 1} {
		code, err := GenerateTOTPCode(seed, time.Unix(step*30, 0).UTC())
		if err != nil {
			t.Fatalf("generate TOTP code: %v", err)
		}

		t.Run("accepted window", func(t *testing.T) {
			// When
			acceptedStep, err := verifier.Verify(code, seed, -1)

			// Then
			if err != nil {
				t.Fatalf("verify TOTP code: %v", err)
			}
			if acceptedStep != step {
				t.Fatal("accepted TOTP step mismatch")
			}
		})
	}
}

func TestTOTP_Verify_rejectsReplayAndOlderSteps(t *testing.T) {
	// Given
	const seed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	clock := func() time.Time { return time.Unix(1111111111, 0).UTC() }
	verifier := NewTOTP(clock, nil)
	currentStep := clock().Unix() / 30

	tests := []struct {
		name string
		step int64
	}{
		{"older step", currentStep - 1},
		{"last accepted step", currentStep},
	}
	for _, test := range tests {
		code, err := GenerateTOTPCode(seed, time.Unix(test.step*30, 0).UTC())
		if err != nil {
			t.Fatalf("generate TOTP code: %v", err)
		}

		t.Run(test.name, func(t *testing.T) {
			// When
			_, err := verifier.Verify(code, seed, currentStep)

			// Then
			if !errors.Is(err, ErrTOTPReplay) {
				t.Fatal("accepted or older TOTP step was not rejected")
			}
		})
	}
}

func TestTOTP_Verify_rejectsStepsOutsideAdjacentWindow(t *testing.T) {
	// Given
	const seed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	clock := func() time.Time { return time.Unix(1111111111, 0).UTC() }
	verifier := NewTOTP(clock, nil)
	currentStep := clock().Unix() / 30

	for _, step := range []int64{currentStep - 2, currentStep + 2} {
		code, err := GenerateTOTPCode(seed, time.Unix(step*30, 0).UTC())
		if err != nil {
			t.Fatalf("generate TOTP code: %v", err)
		}

		t.Run("outside window", func(t *testing.T) {
			// When
			_, err := verifier.Verify(code, seed, -1)

			// Then
			if !errors.Is(err, ErrInvalidTOTP) {
				t.Fatal("TOTP step outside adjacent window was not rejected")
			}
		})
	}
}

func TestTOTP_Verify_acceptsNewerStepWhenAdjacentCodesCollide(t *testing.T) {
	// Given
	const seed = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	const currentStep int64 = 910738
	previousCode, err := GenerateTOTPCode(
		seed,
		time.Unix((currentStep-1)*30, 0).UTC(),
	)
	if err != nil {
		t.Fatalf("generate previous TOTP code: %v", err)
	}
	currentCode, err := GenerateTOTPCode(
		seed,
		time.Unix(currentStep*30, 0).UTC(),
	)
	if err != nil {
		t.Fatalf("generate current TOTP code: %v", err)
	}
	if previousCode != currentCode {
		t.Fatal("adjacent collision fixture no longer collides")
	}
	verifier := NewTOTP(
		func() time.Time { return time.Unix(currentStep*30, 0).UTC() },
		nil,
	)

	// When
	acceptedStep, err := verifier.Verify(currentCode, seed, currentStep-1)

	// Then
	if err != nil {
		t.Fatalf("verify colliding current TOTP code: %v", err)
	}
	if acceptedStep != currentStep {
		t.Fatal("newer colliding TOTP step was not accepted")
	}
}

func TestTOTP_Verify_rejectsMalformedAndWrongInputs(t *testing.T) {
	// Given
	clock := func() time.Time { return time.Unix(1111111111, 0).UTC() }
	verifier := NewTOTP(clock, nil)

	for _, input := range []struct {
		code string
		seed string
	}{
		{"12345", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"},
		{"abcdef", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"},
		{"123456", "invalid"},
	} {
		t.Run("invalid input", func(t *testing.T) {
			// When
			_, err := verifier.Verify(input.code, input.seed, -1)

			// Then
			if !errors.Is(err, ErrInvalidTOTP) {
				t.Fatal("invalid TOTP input was not rejected")
			}
		})
	}
}
