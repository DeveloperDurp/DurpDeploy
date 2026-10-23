package cicontract

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func releaseDeleteAssertions(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("../../scripts/e2e_test.sh")
	if err != nil {
		t.Fatal(err)
	}
	var assertions []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "curl_body ") &&
			strings.Contains(line, "hx-delete=") {
			assertions = append(assertions, strings.TrimSuffix(line, "\\"))
		}
	}
	if len(assertions) != 2 {
		t.Fatalf("found %d release delete assertions", len(assertions))
	}
	return assertions
}

func TestReleaseDeleteAssertionsDrainLargeResponses(t *testing.T) {
	assertions := releaseDeleteAssertions(t)
	for _, scenario := range []string{"present", "missing", "transport-error"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					// Assertions can close the connection before draining it.
					if scenario == "present" {
						_, _ = fmt.Fprintln(
							w,
							`hx-delete="/projects/1/releases/2"`,
						)
					}
					_, _ = fmt.Fprintln(w, strings.Repeat("x", 2<<20))
				},
			))
			t.Cleanup(server.Close)
			if scenario == "transport-error" {
				server.Close()
			}
			for _, assertion := range assertions {
				script := `curl_body() { curl -s "$@"; }
PROJECT_ID=1 WEB_RELEASE_ID=2
WEB_URL="$BASE/projects/1/releases/2"
` + assertion
				cmd := exec.Command("bash", "-euo", "pipefail", "-c", script)
				cmd.Env = append(os.Environ(), "BASE="+server.URL)
				output, err := cmd.CombinedOutput()
				if (err == nil) != (scenario == "present") {
					t.Fatalf("%s: %v: %s", assertion, err, output)
				}
			}
		})
	}
}
