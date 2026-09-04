package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestLogin_Post_RejectsOversizedFormBeforeAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name    string
		chunked bool
	}{
		{name: "declared length"},
		{name: "chunked", chunked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: valid-size credentials plus a large unrelated form field.
			h := newAuthHarness(t)
			body := "email=nope%40x.com&password=anything&padding=" +
				strings.Repeat("a", 1<<20)
			req, err := http.NewRequest(
				http.MethodPost,
				h.server+"/login",
				strings.NewReader(body),
			)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set(
				"Content-Type",
				"application/x-www-form-urlencoded",
			)
			if tc.chunked {
				req.ContentLength = -1
			}

			// When: the oversized form reaches the public login boundary.
			client := newJar(t)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("post /login: %v", err)
			}
			defer resp.Body.Close()

			// Then: parsing rejects it before password verification is queued.
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestSecurityReauth_Post_RejectsOversizedFormBeforeAuthentication(
	t *testing.T,
) {
	// Given: a valid session and CSRF header with an oversized form body.
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	session := seedSession(t, h.repo, h.server, "deployer")
	body := "password=testpass&padding=" + strings.Repeat("a", 1<<20)
	req, err := http.NewRequest(
		http.MethodPost,
		h.server+"/settings/security/reauth",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", session.csrfToken)

	// When: CSRF middleware and the handler inspect the bounded body.
	resp, err := session.client.Do(req)
	if err != nil {
		t.Fatalf("post reauthentication: %v", err)
	}
	defer resp.Body.Close()

	// Then: reauthentication fails before password verification is queued.
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}
