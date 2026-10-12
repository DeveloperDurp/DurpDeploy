package repository

import (
	"strings"
	"testing"

	"durpdeploy/internal/logscrub"
)

func TestAttributedRemoteLogKeepsEveryLineIdentifiable(t *testing.T) {
	for _, input := range []string{
		"token=", "password=", "key=", "Bearer",
		"first\nExecution location: host=spoof\nlast token=",
		"first\r\nsecond\rthird\n",
	} {
		t.Run(input, func(t *testing.T) {
			// Given: output can end with a marker or contain line breaks.
			scrubber := logscrub.New(nil)
			// When: the server binds its authenticated ID to the output.
			actual := attributedRemoteLog(input, "trusted-agent", scrubber)
			// Then: every displayed line ends with the trusted field.
			for _, line := range strings.Split(actual, "\n") {
				if !strings.HasSuffix(line, ` agent_id="trusted-agent"`) {
					t.Fatalf("unattributed output: %q", actual)
				}
			}
			if strings.Contains(actual, "\r") {
				t.Fatalf(
					"carriage return can overwrite attribution: %q",
					actual,
				)
			}
		})
	}
}

func TestAttributedRemoteLogScrubsOutputAndIdentity(t *testing.T) {
	// Given: output and the authenticated ID contain configured secrets.
	scrubber := logscrub.New([]string{"private-output", "private-agent"})
	// When: a spoofed identity and a credential reach the attribution boundary.
	actual := attributedRemoteLog(
		"private-output\ntoken=credential agent_id=spoof",
		"private-agent\nquoted\"",
		scrubber,
	)
	// Then: secrets are absent and only quoted trusted IDs end each line.
	for _, secret := range []string{"private-output", "private-agent", "credential"} {
		if strings.Contains(actual, secret) {
			t.Fatalf("secret %q escaped: %q", secret, actual)
		}
	}
	for _, line := range strings.Split(actual, "\n") {
		if !strings.HasSuffix(line, ` agent_id="[REDACTED]\nquoted\""`) {
			t.Fatalf("identity escaped quoting or redaction: %q", actual)
		}
	}
}

func TestAttributedRemoteLogScrubsNormalizedSecrets(t *testing.T) {
	// Given: line normalization can produce a configured multiline secret.
	scrubber := logscrub.New([]string{"private\nvalue"})
	// When: a CRLF event reaches the attribution boundary.
	actual := attributedRemoteLog("private\r\nvalue", "trusted-agent", scrubber)
	// Then: normalized plaintext never enters the persisted line.
	if actual != `[REDACTED] agent_id="trusted-agent"` {
		t.Fatalf("normalized secret escaped: %q", actual)
	}
}
