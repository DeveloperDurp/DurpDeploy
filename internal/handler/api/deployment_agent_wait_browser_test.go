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

func TestDeploymentWaitingForAgentsBrowserE2E(t *testing.T) {
	// Given: a deployment queued on an agent in maintenance.
	h := newAPIHarness(t)
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
	if _, err := h.repo.Queries.CreateAgent(t.Context(), db.CreateAgentParams{
		ID: "waiting-agent", Name: "Waiting agent", Endpoint: "https://fixture.invalid",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.DB.Exec(`UPDATE agents SET status='active',draining=1,
		certificate_pem='fixture',certificate_fingerprint=hex(zeroblob(32)),
		encrypted_identity='fixture'
		WHERE id='waiting-agent';
		INSERT INTO remote_deployment_claims(deployment_id,agent_id,state)
		VALUES (?,'waiting-agent','waiting')`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	user := seedAPIUser(t, h.repo, "waiting-admin@example.test", "admin")
	if _, err := h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: fmt.Sprint(user.ID), UserID: user.ID, CsrfToken: "waiting-csrf",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.NewRouter(
		h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo),
	))
	t.Cleanup(srv.Close)
	browser := startPackageBrowser(t)
	fleetBrowserSession(t, browser, srv.URL, user.ID)
	fleetBrowserNavigate(
		t,
		browser,
		fmt.Sprintf("%s/deployments/%d", srv.URL, deployment.ID),
	)
	browser.wait(
		t,
		`document.querySelector('#status-badge').innerText.trim() === 'Waiting for agents' && document.querySelectorAll('#status-badge .badge').length === 1`,
	)
	for _, width := range []int{375, 768, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		browser.screenshot(t, fmt.Sprintf("deployment-%d-waiting", width))
	}

	// When: an agent claims the waiting work.
	if _, err := h.repo.DB.Exec(`UPDATE remote_deployment_claims
		SET state='claimed',claim_token_hash=zeroblob(32),ciphertext='fixture',
		claim_expires_at=unixepoch()+60,last_heartbeat_at=unixepoch()
		WHERE deployment_id=?`, deployment.ID); err != nil {
		t.Fatal(err)
	}

	// Then: status polling clears the message without a page reload.
	browser.wait(
		t,
		`!document.querySelector('#status-badge').innerText.includes('Waiting for agents')`,
	)
	browser.wait(
		t,
		`document.querySelector('#status-badge').innerText.trim() === 'running'`,
	)
	for _, width := range []int{375, 768, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		browser.screenshot(t, fmt.Sprintf("deployment-%d-claimed", width))
	}
}
