package auth_test

import (
	"strings"
	"testing"

	"durpdeploy/internal/auth"
)

func TestSafeLoginDestination(t *testing.T) {
	for _, value := range []string{
		"", "https://evil.test/", "//evil.test/", "///evil.test/",
		`/\evil.test`, "/%5cevil.test", "/%2fevil.test", "/%00evil",
		"/%0d%0aLocation:evil", "/%zz", "/projects#fragment",
		"/login", "/login?next=/projects", "/login/mfa", "/logout",
		"/projects/../login", "/%2e%2e/login/mfa", "/a/../logout",
		"/" + strings.Repeat("x", 2048), "/" + strings.Repeat("é", 500),
	} {
		t.Run(value[:min(len(value), 40)], func(t *testing.T) {
			if got := auth.SafeLoginDestination(value); got != "/" {
				t.Fatalf("unsafe destination returned %q", got)
			}
		})
	}
	for _, value := range []string{
		"/projects/12", "/deployments?project_id=12&status=failed",
		"/projects/12?name=one%20two", "/projects/%C3%A9",
	} {
		if got := auth.SafeLoginDestination(value); got != value {
			t.Fatalf("local destination = %q, want %q", got, value)
		}
	}
}
