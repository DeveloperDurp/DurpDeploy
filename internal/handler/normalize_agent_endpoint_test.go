package handler

import "testing"

func TestNormalizeAgentEndpoint_UsesAgentBootstrapPort_whenPortIsBlank(
	t *testing.T,
) {
	// Given
	host := "agent.example.test"
	port := ""

	// When
	endpoint, err := normalizeAgentEndpoint(host, port)

	// Then
	if err != nil {
		t.Fatalf(
			"normalizeAgentEndpoint(%q, %q) error = %v, want nil",
			host,
			port,
			err,
		)
	}
	if endpoint != "https://agent.example.test:10943" {
		t.Fatalf(
			"normalizeAgentEndpoint(%q, %q) = %q, want %q",
			host,
			port,
			endpoint,
			"https://agent.example.test:10943",
		)
	}
}
