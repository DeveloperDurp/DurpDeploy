package runner

import "durpdeploy/internal/logscrub"

type Scrubber = logscrub.Scrubber

var commonSecretPatterns []string

func NewScrubber(secrets []string, patterns ...string) *Scrubber {
	if len(commonSecretPatterns) != 0 {
		additional := append([]string(nil), commonSecretPatterns...)
		return logscrub.NewWithPatterns(
			secrets,
			append(additional, patterns...),
		)
	}
	return logscrub.New(secrets, patterns...)
}
