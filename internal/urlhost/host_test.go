package urlhost_test

import (
	"testing"

	"durpdeploy/internal/urlhost"
)

func TestParseRejectsAmbiguousAndLocalHosts(t *testing.T) {
	for _, host := range []string{
		"", "localhost", "LOCALHOST.", "service.localhost.",
		"2130706433", "0x7f000001", "0X7F.0.0.1", "0177.0.0.1",
		"127.1", "127.0.1", "127.000.0.1", "0x7f.0.0.1.",
		"2852039166", "0xa9.0xfe.0xa9.0xfe", "0251.0376.0251.0376",
		"134744072", "010.010.010.010", "999.999.999.999",
		"fe80::1%eth0", "2001:4860:4860::8888%eth0", "::invalid",
	} {
		t.Run(host, func(t *testing.T) {
			if _, _, err := urlhost.Parse(host); err == nil {
				t.Fatal("expected rejected host")
			}
		})
	}
}

func TestParsePreservesDNSAndCanonicalLiterals(t *testing.T) {
	for _, host := range []string{
		"Example.COM.", "host.containers.internal", "2130706433.example",
		"0x7f.example", "example.123", "8.8.8.8", "8.8.8.8.",
		"10.0.0.1", "127.0.0.1.", "::ffff:127.0.0.1",
		"2001:4860:4860::8888",
	} {
		t.Run(host, func(t *testing.T) {
			if _, _, err := urlhost.Parse(host); err != nil {
				t.Fatal(err)
			}
		})
	}
}
