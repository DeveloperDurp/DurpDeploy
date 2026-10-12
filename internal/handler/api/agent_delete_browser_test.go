//go:build e2e && agentbrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
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
	if _, err := h.repo.Queries.AddAgentLabel(t.Context(), db.AddAgentLabelParams{
		AgentID: "pending-delete", Label: strings.Repeat("longlabel", 7),
	}); err != nil {
		t.Fatal(err)
	}
	longEnvironment, err := h.repo.Queries.CreateEnvironment(t.Context(),
		db.CreateEnvironmentParams{Name: strings.Repeat("LongEnvironment", 6)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.Queries.AddAgentEnvironmentLabel(t.Context(),
		db.AddAgentEnvironmentLabelParams{
			AgentID: "pending-delete", EnvironmentID: longEnvironment.ID,
		}); err != nil {
		t.Fatal(err)
	}
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
	checkAgentDeleteBrowserLabels(t, browser)

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
	fleetRevokeConfirm(t, browser, "busy-delete")
	browser.wait(t, `document.body.innerText.includes('revoked')`)
	agent, err = h.repo.Queries.GetAgent(t.Context(), "busy-delete")
	if err != nil || agent.Status != "revoked" {
		t.Fatalf("emergency revocation agent=%+v error=%v", agent, err)
	}
}

func fleetRevokeConfirm(t *testing.T, browser *packageBrowser, id string) {
	t.Helper()
	browser.wire.events = nil
	browser.evaluate(t, fmt.Sprintf(`setTimeout(() => document.querySelector(
		'[hx-post="/admin/agents/%s/revoke"]').click(), 0); true`, id))
	if err := browser.wire.waitEvent("Page.javascriptDialogOpening", browser.session); err != nil {
		t.Fatal(err)
	}
	browser.call(
		t,
		"Page.handleJavaScriptDialog",
		map[string]bool{"accept": true},
		&struct{}{},
	)
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

func checkAgentDeleteBrowserLabels(t *testing.T, browser *packageBrowser) {
	t.Helper()
	for _, width := range []int{375, 768, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		if string(
			browser.evaluate(t, `Array.from(document.querySelectorAll('.badge'))
			.every(el => el.scrollWidth <= el.clientWidth + 2)`),
		) != "true" {
			t.Fatalf("agent labels overflow at width %d", width)
		}
		browser.screenshot(t, fmt.Sprintf("delete-rest-labels-%d", width))
		browser.evaluate(
			t,
			`document.querySelector('button[hx-post$="/delete"]').scrollIntoView(); true`,
		)
		var target struct{ X, Y float64 }
		if err := json.Unmarshal(browser.evaluate(t, `(() => {
			const r=document.querySelector('button[hx-post$="/delete"]').getBoundingClientRect();
			return {X:r.x+r.width/2,Y:r.y+r.height/2};
		})()`), &target); err != nil {
			t.Fatal(err)
		}
		browser.call(t, "Input.dispatchMouseEvent", map[string]any{
			"type": "mouseMoved", "x": target.X, "y": target.Y,
		}, &struct{}{})
		browser.screenshot(t, fmt.Sprintf("delete-hover-%d", width))
		browser.screenshot(t, fmt.Sprintf("delete-hover-settled-%d", width))
		browser.evaluate(
			t,
			`document.querySelector('button[hx-post$="/delete"]').focus(); true`,
		)
		browser.screenshot(t, fmt.Sprintf("delete-focused-%d", width))
	}
}
