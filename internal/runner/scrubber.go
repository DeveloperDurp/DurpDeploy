package runner

import "durpdeploy/internal/logscrub"

type Scrubber = logscrub.Scrubber

var commonSecretPatterns []string

func NewScrubber(secrets []string) *Scrubber {
	if len(commonSecretPatterns) != 0 {
		return logscrub.NewWithPatterns(secrets, commonSecretPatterns)
	}
	return logscrub.New(secrets)
}
