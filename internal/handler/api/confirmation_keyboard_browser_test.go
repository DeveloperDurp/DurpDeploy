//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestConfirmationKeyboardBrowserE2E(t *testing.T) {
	f := newUsersPhoneFixture(t)
	for _, id := range []string{"keyboard-dialog", "other-factor"} {
		if _, err := f.h.repo.Queries.CreateWebAuthnCredential(t.Context(),
			db.CreateWebAuthnCredentialParams{
				CredentialID: []byte(id), UserID: f.adminID,
				Name: id + strings.Repeat("Long passkey name ", 12), PublicKey: []byte{2},
				TransportsJson: "[]", AttestationType: "none", AttestationFormat: "none",
			}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.h.repo.Queries.UpdateUser(t.Context(), db.UpdateUserParams{
		ID: f.users[0].ID, Role: f.users[0].Role,
		Name: strings.Repeat("LongName", 32),
	}); err != nil {
		t.Fatal(err)
	}
	project, err := f.h.repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{
			Name: strings.Repeat("Project name ", 45),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := f.h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: strings.Repeat("release ", 40), StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	for _, width := range []int{390, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 844, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.server.URL+"/settings/security")
		for _, opener := range []string{
			"[data-passkey-delete-dialog] button", "[data-security-disable-opener]",
		} {
			b.evaluate(
				t,
				fmt.Sprintf(`document.querySelector(%q).click(); true`, opener),
			)
			b.wait(
				t,
				`document.querySelector('dialog[open]')?.matches(':modal')`,
			)
			assertDialogKeyboardBounds(t, b, "dialog[open]")
			b.evaluate(
				t,
				`document.querySelector('dialog[open] .modal-action form[method=dialog] button').click(); true`,
			)
			b.wait(t, `!document.querySelector('dialog[open]')`)
		}
		b.navigateBackTest(t, fmt.Sprintf("%s/projects/%d/releases/%d",
			f.server.URL, project.ID, release.ID))
		b.evaluate(
			t,
			`document.querySelector('dialog[x-ref=confirmDelete]').showModal(); true`,
		)
		assertDialogKeyboardBounds(t, b, "dialog[open]")
		b.evaluate(
			t,
			`document.querySelector('dialog[open] .modal-action button').click(); true`,
		)
		b.wait(t, `!document.querySelector('dialog[open]')`)
		b.navigateBackTest(t, f.server.URL+"/admin/users")
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('[data-user-id="%d"] [data-mfa-reset-opener]').click(); true`,
				f.users[0].ID,
			),
		)
		b.wait(t, `document.querySelector('dialog[open]')?.matches(':modal')`)
		assertDialogKeyboardBounds(t, b, "dialog[open]")
		b.evaluate(
			t,
			`document.querySelector('dialog[open] .modal-action button').click(); true`,
		)
		b.wait(t, `!document.querySelector('dialog[open]')`)
		b.navigateBackTest(
			t,
			fmt.Sprintf("%s/projects/%d", f.server.URL, project.ID),
		)
		b.evaluate(
			t,
			`document.querySelector('[aria-controls="project-menu-dialog"]').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('#project-menu-dialog').matches(':modal')`,
		)
		assertDialogKeyboardBounds(t, b, "#project-menu-dialog")
		b.evaluate(
			t,
			`document.querySelector('#project-menu-dialog button[aria-label="Close project menu"]').click(); true`,
		)
		b.wait(t, `!document.querySelector('#project-menu-dialog').open`)
	}
	credentials, err := f.h.repo.Queries.ListWebAuthnCredentialsByUserID(
		t.Context(),
		f.adminID,
	)
	if err != nil || len(credentials) != 2 {
		t.Fatalf("cancelled confirmation changed credentials: %v", err)
	}
	if _, err := f.h.repo.Queries.GetRelease(t.Context(), release.ID); err != nil {
		t.Fatalf("cancelled confirmation deleted release: %v", err)
	}
}
