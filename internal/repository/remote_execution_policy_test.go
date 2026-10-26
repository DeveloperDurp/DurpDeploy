package repository_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/config"
)

func TestCustomExecutionPolicyBlocksRemoteQueueAndClaims(t *testing.T) {
	for _, limits := range []config.ExecutionLimits{
		{MaxRetries: 10, MaxTimeoutSeconds: 86400, DefaultTimeoutSeconds: 2},
		{MaxRetries: 10, MaxTimeoutSeconds: 86400, DefaultTimeoutSeconds: 600},
		{MaxRetries: 0, MaxTimeoutSeconds: 86400, DefaultTimeoutSeconds: 300},
		{MaxRetries: 11, MaxTimeoutSeconds: 86400, DefaultTimeoutSeconds: 300},
		{MaxRetries: 10, MaxTimeoutSeconds: 600, DefaultTimeoutSeconds: 300},
		{MaxRetries: 10, MaxTimeoutSeconds: 172800, DefaultTimeoutSeconds: 300},
	} {
		t.Run(fmt.Sprint(limits), func(t *testing.T) {
			settings := config.Defaults()
			settings.Limits.Execution = limits
			testCustomResourcePolicyBlocksRemoteQueueAndClaims(
				t,
				settings,
				false,
			)
		})
	}
}
