package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestUsers_MFAResetConfirmation_whenListingAnotherUser(t *testing.T) {
	// Given an admin and another user in the list.
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	admin := seedSessionAs(
		t,
		h.repo,
		h.server,
		"mfa-reset-admin@example.com",
		"admin",
	)
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"mfa-reset-target@example.com",
		"deployer",
	)

	// When the admin loads the users list.
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		h.server+"/admin/users",
		nil,
	)
	if err != nil {
		t.Fatalf("build users request: %v", err)
	}
	resp, err := admin.client.Do(req)
	if err != nil {
		t.Fatalf("GET /admin/users: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)

	// Then the reset form opens an accessible confirmation dialog.
	actionPath := fmt.Sprintf("/admin/users/%d/mfa-reset", target.user.ID)
	action := fmt.Sprintf(`action="%s"`, actionPath)
	dialogID := fmt.Sprintf("mfa-reset-dialog-%d", target.user.ID)
	if !strings.Contains(
		body,
		`data-mfa-reset-dialog="`+dialogID+`"`,
	) {
		t.Fatalf("body missing dialog trigger %q: %s", dialogID, body)
	}
	if !strings.Contains(body, `<dialog id="`+dialogID+`"`) ||
		!strings.Contains(
			body,
			`aria-labelledby="`+
				fmt.Sprintf("mfa-reset-title-%d", target.user.ID)+`"`,
		) || !strings.Contains(
		body,
		`aria-describedby="`+
			fmt.Sprintf("mfa-reset-description-%d", target.user.ID)+`"`,
	) {
		t.Fatalf("body missing accessible reset dialog: %s", body)
	}
	if !strings.Contains(body, target.user.Email) {
		t.Fatalf(
			"body missing reset target name %q: %s",
			target.user.Email,
			body,
		)
	}
	if !strings.Contains(body, `method="dialog"`) ||
		!strings.Contains(body, `>Cancel</button>`) ||
		!strings.Contains(body, `data-mfa-reset-confirm>`) ||
		!strings.Contains(body, `>Confirm reset</button>`) {
		t.Fatalf("body missing explicit modal actions: %s", body)
	}
	csrfField := fmt.Sprintf(
		`name="csrf_token" value="%s"`,
		admin.csrfToken,
	)
	targetForms := make([]string, 0, 2)
	for _, fragment := range strings.Split(body, "<form") {
		if strings.Contains(fragment, action) {
			targetForms = append(targetForms, fragment)
		}
	}
	if len(targetForms) != 2 {
		t.Fatalf("reset forms = %d, want 2: %s", len(targetForms), body)
	}
	for _, form := range targetForms {
		if !strings.Contains(form, csrfField) || !strings.Contains(
			form,
			`name="reason" value="administrative_reset"`,
		) {
			t.Fatalf("reset form missing canonical fields: %s", form)
		}
	}

	// When JavaScript is unavailable, the row form submits directly.
	resetResponse, err := admin.client.PostForm(
		h.server+actionPath,
		url.Values{
			"csrf_token": {admin.csrfToken},
			"reason":     {"administrative_reset"},
		},
	)
	if err != nil {
		t.Fatalf("POST no-JS reset fallback: %v", err)
	}
	defer resetResponse.Body.Close()

	// Then CSRF accepts the request and the reset returns to the users list.
	if resetResponse.StatusCode != http.StatusSeeOther ||
		resetResponse.Header.Get("Location") != "/admin/users" {
		t.Fatalf(
			"no-JS reset = %d %q, want 303 users list",
			resetResponse.StatusCode,
			resetResponse.Header.Get("Location"),
		)
	}
}
