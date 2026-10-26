package repository_test

import (
	"testing"

	"durpdeploy/internal/config"
)

func TestCustomStagingPolicyBlocksRemoteQueueAndClaims(t *testing.T) {
	for _, field := range []string{"payload lower", "payload higher", "entries lower", "entries higher", "archive lower", "archive higher"} {
		t.Run(field, func(t *testing.T) {
			settings := config.Defaults()
			switch field {
			case "payload lower":
				settings.Limits.Staging.PayloadBytes = 1024
			case "payload higher":
				settings.Limits.Staging.PayloadBytes++
			case "entries lower":
				settings.Limits.Staging.Entries = 2
			case "entries higher":
				settings.Limits.Staging.Entries++
			case "archive lower":
				settings.Limits.Staging.ArchiveBytes--
			case "archive higher":
				settings.Limits.Staging.ArchiveBytes++
			}
			testCustomResourcePolicyBlocksRemoteQueueAndClaims(
				t,
				settings,
				false,
			)
		})
	}
}
