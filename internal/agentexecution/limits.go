package agentexecution

import (
	"errors"
	"fmt"

	"durpdeploy/internal/config"
)

const (
	MaxRetries        int64 = 10
	MaxTimeoutSeconds int64 = 24 * 60 * 60
)

var ErrLimits = errors.New("invalid step execution limits")

func ValidateLimits(timeoutSeconds, maxRetries int64) error {
	return ValidateLimitsWithPolicy(
		timeoutSeconds,
		maxRetries,
		config.Defaults().Limits.Execution,
	)
}

// ValidateLimitsWithPolicy checks seconds before any duration conversion.
// The policy must come from the validated startup snapshot.
func ValidateLimitsWithPolicy(
	timeoutSeconds, maxRetries int64,
	limits config.ExecutionLimits,
) error {
	switch {
	case timeoutSeconds < 0:
		return fmt.Errorf(
			"%w: timeout must be a non-negative integer",
			ErrLimits,
		)
	case timeoutSeconds > limits.MaxTimeoutSeconds:
		return fmt.Errorf(
			"%w: timeout must not exceed %d seconds",
			ErrLimits,
			limits.MaxTimeoutSeconds,
		)
	case maxRetries < 0:
		return fmt.Errorf(
			"%w: max retries must be a non-negative integer",
			ErrLimits,
		)
	case maxRetries > limits.MaxRetries:
		return fmt.Errorf(
			"%w: max retries must not exceed %d",
			ErrLimits,
			limits.MaxRetries,
		)
	default:
		return nil
	}
}
