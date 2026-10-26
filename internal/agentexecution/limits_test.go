package agentexecution

import (
	"errors"
	"math"
	"testing"

	"durpdeploy/internal/config"
)

func TestExecutionLimits(t *testing.T) {
	for _, tc := range []struct {
		name             string
		timeout, retries int64
		valid            bool
	}{
		{"defaults", 0, 0, true},
		{"maximum", MaxTimeoutSeconds, MaxRetries, true},
		{"negative timeout", -1, 0, false},
		{"negative retries", 0, -1, false},
		{"timeout exceeded", MaxTimeoutSeconds + 1, 0, false},
		{"retries exceeded", 0, MaxRetries + 1, false},
		{"int64 timeout", math.MaxInt64, 0, false},
		{"int64 retries", 0, math.MaxInt64, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLimits(tc.timeout, tc.retries)
			if (err == nil) != tc.valid ||
				(!tc.valid && !errors.Is(err, ErrLimits)) {
				t.Fatalf("limits error=%v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestConfiguredExecutionLimits(t *testing.T) {
	for _, limits := range []config.ExecutionLimits{
		{MaxRetries: 0, MaxTimeoutSeconds: 3, DefaultTimeoutSeconds: 3},
		{MaxRetries: 11, MaxTimeoutSeconds: 172800, DefaultTimeoutSeconds: 300},
	} {
		for _, tc := range []struct {
			timeout, retries int64
			valid            bool
		}{
			{0, 0, true}, {limits.MaxTimeoutSeconds, limits.MaxRetries, true},
			{-1, 0, false}, {0, -1, false},
			{limits.MaxTimeoutSeconds + 1, 0, false}, {0, limits.MaxRetries + 1, false},
			{math.MaxInt64, 0, false}, {0, math.MaxInt64, false},
		} {
			err := ValidateLimitsWithPolicy(tc.timeout, tc.retries, limits)
			if (err == nil) != tc.valid ||
				(!tc.valid && !errors.Is(err, ErrLimits)) {
				t.Fatalf(
					"policy=%+v timeout=%d retries=%d valid=%v: %v",
					limits,
					tc.timeout,
					tc.retries,
					tc.valid,
					err,
				)
			}
		}
	}
}
