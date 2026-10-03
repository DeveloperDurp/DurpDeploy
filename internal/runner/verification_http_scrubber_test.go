package runner

import (
	"testing"
)

func TestVerificationHTTPScrubberPreservesStreamRedaction(t *testing.T) {
	for _, test := range []struct {
		target  string
		secrets []string
	}{
		{"https://stored-host-credential.hooks.invalid.:8443/probe",
			[]string{"StOrEd-HoSt-CrEdEnTiAl"}},
		{"https://api.apikey-123456789.hooks.invalid/probe",
			[]string{"APIKEY-123456789", "apikey-123456789"}},
		{"https://b%C3%BCcher.hooks.invalid:8443/probe",
			[]string{"BÜCHER", "XN--BCHER-KVA"}},
		{"https://xn--bcher-kva.hooks.invalid:8443/probe",
			[]string{"BÜCHER", "XN--BCHER-KVA"}},
		{"https://service.invalid/probe?mode=ready&private-proof=enabled",
			[]string{"private-proof"}},
		{"https://service.invalid/probe?private-proof&mode=ready",
			[]string{"private-proof"}},
		{"https://service.invalid/probe?mode=ready&private%2fproof+value=enabled",
			[]string{"private/proof value", "private%2fproof+value",
				"private%2Fproof+value", "private%2Fproof%20value"}},
		{"https://service.invalid/probe?private-proof=%zz&mode=ready",
			[]string{"private-proof", "%zz"}},
		{"https://service.invalid/probe?private%3bproof=value&mode=ready",
			[]string{"private;proof", "private%3Bproof"}},
		{"https://service.invalid/probe?private;proof=value&mode=ready",
			[]string{"private;proof"}},
		{"https://service.invalid/probe?mode=ready&private%zz=value",
			[]string{"private%zz"}},
		{"https://service.invalid/probe?mode=ready&private%0a%E2%98%83=enabled",
			[]string{"private\n☃", "private%0A%E2%98%83"}},
		{"https://service.invalid/probe?mode=ready&private%FFproof=enabled",
			[]string{"private\xffproof", "private�proof"}},
	} {
		t.Run(test.target, func(t *testing.T) {
			scrubber := verificationHTTPScrubber(
				test.target,
				[]string{"release-secret", "API"},
			)
			for _, secret := range test.secrets {
				text := "healthy " + secret + " release-secret Bearer example-proof\n"
				var emitted, pending string
				for _, next := range []byte(text) {
					pending += string([]byte{next})
					safeEnd := len(pending) - scrubber.PendingBytes(pending)
					emitted += scrubber.Scrub(pending[:safeEnd])
					pending = pending[safeEnd:]
				}
				got := emitted + scrubber.Scrub(pending)
				if got != "healthy [REDACTED] [REDACTED] [REDACTED]\n" {
					t.Fatalf("streamed output=%q", got)
				}
			}
		})
	}
	for _, target := range []string{
		"http://10.20.30.200:8080/probe", "http://[fd00::200]:8080/probe",
	} {
		got := verificationHTTPScrubber(target, nil).Scrub("HTTP status: 200\n")
		if got != "HTTP status: 200\n" {
			t.Fatalf("numeric host redacted status: %q", got)
		}
	}
}
