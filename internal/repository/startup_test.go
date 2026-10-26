package repository

import (
	"testing"

	"durpdeploy/internal/config"
)

func TestConfiguredRepositoryIsolatesStartupPolicies(t *testing.T) {
	settings, err := config.LoadWithLookup(func(key string) (string, bool) {
		if key == "DURPDEPLOY_EXTRA_SCRUB_PATTERNS" {
			return "configured-proof", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewConfigured(nil, settings)
	settings.ExtraScrubPatterns = "changed-proof"
	t.Setenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS", "environment-proof")
	got := repo.NewScrubber([]string{"release-proof"}).Scrub(
		"configured-proof environment-proof release-proof Bearer common-proof",
	)
	if got != "[REDACTED] environment-proof [REDACTED] [REDACTED]" {
		t.Fatal("configured redaction changed with the process environment")
	}
	other := NewConfigured(nil, config.Defaults())
	if other.NewScrubber(nil).Scrub("configured-proof") != "configured-proof" {
		t.Fatal("configured patterns leaked into another repository")
	}
	legacy := New(nil)
	if legacy.NewScrubber(nil).Scrub("environment-proof") != "[REDACTED]" {
		t.Fatal("default test constructor lost environment compatibility")
	}
}
