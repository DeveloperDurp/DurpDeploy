//go:build e2e && agentbrowser

package api_test

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestAgentDeleteBrowserE2E(t *testing.T) {
	// Given: disposable pending/revoked agents and an agent with issued work.
	h := newAPIHarness(t)
	for _, id := range []string{"pending-delete", "revoked-delete", "busy-delete"} {
		if _, err := h.repo.Queries.CreateAgent(t.Context(), db.CreateAgentParams{
			ID: id, Name: id, Endpoint: "https://fixture.invalid/" + id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.repo.RevokeAgent(t.Context(), "revoked-delete"); err != nil {
		t.Fatal(err)
	}
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	deployment := seedDeployment(
		t,
		h.repo,
		release.ID,
		environment.ID,
		"running",
	)
	if _, err := h.repo.DB.Exec(`UPDATE deployments
		SET assigned_agent_id='busy-delete' WHERE id=?`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.DB.Exec(`INSERT INTO remote_deployment_claims
		(deployment_id,agent_id,state,claim_token_hash,ciphertext,
		 claim_expires_at,last_heartbeat_at,started_at)
		VALUES (?,'busy-delete','started',zeroblob(32),'fixture',
		 unixepoch()+60,unixepoch(),unixepoch())`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	user := seedAPIUser(t, h.repo, "delete-browser@example.test", "admin")
	if _, err := h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: fmt.Sprint(user.ID), UserID: user.ID, CsrfToken: "delete-csrf",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	browser := startPackageBrowser(t)
	fleetBrowserSession(t, browser, srv.URL, user.ID)
	fleetBrowserNavigate(t, browser, srv.URL+"/admin/agents/pending-delete")
	for _, width := range []int{375, 768, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		browser.evaluate(
			t,
			`document.querySelector('button[hx-post$="/delete"]').focus(); true`,
		)
		browser.screenshot(t, fmt.Sprintf("delete-focused-%d", width))
	}

	// When: the native confirmation is cancelled, then accepted on each page.
	fleetDeleteConfirm(t, browser, "pending-delete", false)
	browser.wait(t, `location.pathname === '/admin/agents/pending-delete'`)
	fleetDeleteConfirm(t, browser, "pending-delete", true)
	fleetBrowserLoaded(t, browser)
	browser.wait(t, `location.pathname === '/admin/agents' &&
		!document.querySelector('[hx-post="/admin/agents/pending-delete/delete"]')`)
	fleetDeleteConfirm(t, browser, "revoked-delete", true)
	fleetBrowserLoaded(t, browser)
	browser.wait(
		t,
		`!document.querySelector('[hx-post="/admin/agents/revoked-delete/delete"]')`,
	)
	fleetDeleteConfirm(t, browser, "busy-delete", true)
	browser.wait(
		t,
		`document.querySelector('[x-data="toast"]')?.innerText.includes('unresolved remote work')`,
	)

	// Then: blocked deletion stays on the page and leaves inventory intact.
	if string(browser.evaluate(t, `location.pathname === '/admin/agents' &&
		document.querySelector('[hx-post="/admin/agents/busy-delete/delete"]') !== null`)) != "true" {
		t.Fatal("blocked delete changed the page or removed its agent")
	}
	agent, err := h.repo.Queries.GetAgent(t.Context(), "busy-delete")
	if err != nil || agent.Status != "pending" {
		t.Fatalf("blocked agent=%+v error=%v", agent, err)
	}
	for _, width := range []int{375, 768, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		browser.screenshot(t, fmt.Sprintf("delete-blocked-list-%d", width))
	}
}

func fleetDeleteConfirm(
	t *testing.T,
	browser *packageBrowser,
	id string,
	accept bool,
) {
	t.Helper()
	browser.wire.events = nil
	browser.evaluate(t, fmt.Sprintf(`setTimeout(() => document.querySelector(
		'[hx-post="/admin/agents/%s/delete"]').click(), 0); true`, id))
	if err := browser.wire.waitEvent("Page.javascriptDialogOpening", browser.session); err != nil {
		t.Fatal(err)
	}
	browser.call(
		t,
		"Page.handleJavaScriptDialog",
		map[string]bool{"accept": accept},
		&struct{}{},
	)
}
