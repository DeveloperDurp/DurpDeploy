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
		"first\u2028spoof\u2029last\u0085next\vvertical\fform",
	} {
		t.Run(input, func(t *testing.T) {
			// Given: output can end with a marker or contain line breaks.
			scrubber := logscrub.New(nil)
			// When: the server binds its authenticated ID to the output.
			actual := attributedRemoteLog(input, "trusted-agent", scrubber)
			// Then: every displayed line ends with the trusted field.
			if !strings.HasSuffix(actual, ` agent_id="trusted-agent"`) {
				t.Fatalf("unattributed output: %q", actual)
			}
			if strings.ContainsAny(actual, "\r\n\v\f\u0085\u2028\u2029") {
				t.Fatalf(
					"line separator escaped attribution: %q",
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

func TestAttributedRemoteLogScrubsRawMultilineSecrets(t *testing.T) {
	// Given: a configured secret contains raw CRLF line separators.
	scrubber := logscrub.New([]string{"private\r\nvalue"})
	// When: a CRLF event reaches the attribution boundary.
	actual := attributedRemoteLog("private\r\nvalue", "trusted-agent", scrubber)
	// Then: scrubbing precedes escaping and removes the complete secret.
	if actual != `[REDACTED] agent_id="trusted-agent"` {
		t.Fatalf("multiline secret escaped: %q", actual)
	}
}

func TestAttributedRemoteLogBoundsSeparatorExpansion(t *testing.T) {
	// Given: a permitted event can consist entirely of line separators.
	input := strings.Repeat("\n\r\u0085\u2028\u2029", 10000)
	// When: the server escapes them and appends one authenticated ID.
	actual := attributedRemoteLog(input, "trusted-agent", logscrub.New(nil))
	// Then: escaping costs at most three bytes per input byte plus one suffix.
	if len(actual) > 3*len(input)+len(` agent_id="trusted-agent"`) ||
		strings.Count(actual, `agent_id="trusted-agent"`) != 1 {
		t.Fatalf(
			"unbounded attribution: input=%d output=%d",
			len(input),
			len(actual),
		)
	}
}

func TestRemoteAttributionRetainsCrossEventSecretRedaction(t *testing.T) {
	for _, parts := range [][2]string{
		{"first\n", "second"},
		{"first\r", "\nsecond"},
		{"first\u2028", "second"},
	} {
		t.Run(parts[0], func(t *testing.T) {
			// Given: a raw multiline secret ends in another remote event.
			scrubber := logscrub.New([]string{parts[0] + parts[1]})
			ready, pending, err := prepareRemoteLogEvents(nil,
				[]RemoteLogEvent{{Sequence: 1, Line: parts[0]}},
				-1, scrubber, false)
			if err != nil || len(ready) != 0 || len(pending) != 1 {
				t.Fatalf(
					"partial secret escaped buffer: ready=%+v error=%v",
					ready,
					err,
				)
			}
			// When: terminal flushing receives the remaining secret bytes.
			ready, _, err = prepareRemoteLogEvents(pending,
				[]RemoteLogEvent{{Sequence: 2, Line: parts[1]}},
				-1, scrubber, true)
			if err != nil || len(ready) != 2 {
				t.Fatalf("flush=%+v error=%v", ready, err)
			}
			// Then: attribution cannot restore either raw secret fragment.
			for _, event := range ready {
				line := attributedRemoteLog(
					event.Line,
					"trusted-agent",
					scrubber,
				)
				if strings.Contains(line, "first") ||
					strings.Contains(line, "second") ||
					!strings.HasSuffix(line, ` agent_id="trusted-agent"`) {
					t.Fatalf("cross-event secret escaped: %q", line)
				}
			}
		})
	}
}
