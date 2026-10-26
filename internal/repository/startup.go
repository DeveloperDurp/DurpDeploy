package repository

import "durpdeploy/internal/logscrub"

// NewScrubber applies the captured startup policy to logs and public previews.
func (r *Repository) NewScrubber(
	secrets []string,
	patterns ...string,
) *logscrub.Scrubber {
	extra, _ := r.StartupLookup("DURPDEPLOY_EXTRA_SCRUB_PATTERNS")
	return logscrub.NewConfigured(secrets, extra, patterns...)
}
