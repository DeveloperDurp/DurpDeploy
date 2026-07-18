package runner

import "testing"

func TestScrubber_LiteralSecret(t *testing.T) {
	s := NewScrubber([]string{"s3cr3t-value"})
	got := s.Scrub("connecting with password s3cr3t-value now")
	want := "connecting with password [REDACTED] now"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_MultilineLiteralSecret(t *testing.T) {
	key := "-----BEGIN KEY-----\nAAAA\nBBBB\n-----END KEY-----"
	s := NewScrubber([]string{key})
	got := s.Scrub("here is the key:\n" + key + "\ndone")
	want := "here is the key:\n[REDACTED]\ndone"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_OverlappingSecrets_LongestFirst(t *testing.T) {
	s := NewScrubber([]string{"SECRET", "SECRET_EXTENDED"})
	got := s.Scrub("value=SECRET_EXTENDED")
	want := "value=[REDACTED]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_SubstringSecrets(t *testing.T) {
	s := NewScrubber([]string{"PASSWORD", "PASS"})
	got := s.Scrub("PASSWORD and PASS both present")
	want := "[REDACTED] and [REDACTED] both present"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_EmptySecretsIgnored(t *testing.T) {
	s := NewScrubber([]string{"", "real-secret"})
	got := s.Scrub("nothing to see, real-secret here")
	want := "nothing to see, [REDACTED] here"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_NoSecretsIsNoop(t *testing.T) {
	s := NewScrubber(nil)
	text := "plain log line with no secrets"
	if got := s.Scrub(text); got != text {
		t.Fatalf("got %q, want %q", got, text)
	}
}

func TestScrubber_BearerToken(t *testing.T) {
	s := NewScrubber(nil)
	got := s.Scrub(`curl -H "Authorization: Bearer abc.123-XYZ_456="`)
	want := `curl -H "Authorization: [REDACTED]"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_GitHubToken(t *testing.T) {
	s := NewScrubber(nil)
	token := "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	got := s.Scrub("token: " + token)
	want := "token: [REDACTED]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_AWSAccessKey(t *testing.T) {
	s := NewScrubber(nil)
	got := s.Scrub("AWS_ACCESS_KEY_ID=AKIAABCDEFGHIJKLMNOP")
	want := "AWS_ACCESS_KEY_ID=[REDACTED]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_SlackToken(t *testing.T) {
	s := NewScrubber(nil)
	got := s.Scrub("slack token xoxb-1234-5678-abcdEFGH")
	want := "slack token [REDACTED]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_AssignmentPattern(t *testing.T) {
	s := NewScrubber(nil)
	got := s.Scrub(`export PASSWORD=hunter2 and TOKEN=abc123`)
	want := `export [REDACTED] and [REDACTED]`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScrubber_NoFalsePositives(t *testing.T) {
	s := NewScrubber([]string{"real-secret"})
	text := "deploying release 42 to environment staging, all good"
	if got := s.Scrub(text); got != text {
		t.Fatalf("got %q, want %q (unexpected redaction)", got, text)
	}
}

func TestScrubber_CustomPattern(t *testing.T) {
	// Save original and restore after test
	old := commonSecretPatterns
	defer func() { commonSecretPatterns = old }()

	commonSecretPatterns = append(commonSecretPatterns, `CUSTOM_PAT_[0-9]+`)
	s := NewScrubber(nil)
	got := s.Scrub("connecting to CUSTOM_PAT_12345 now")
	want := "connecting to [REDACTED] now"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
