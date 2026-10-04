//go:build e2e && agentbrowser

package api_test

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestAgentFleetBrowserE2E(t *testing.T) {
	// Given: paired, draining, drained, disabled, offline, and revoked agents.
	h := newAPIHarness(t)
	for i, state := range []string{"active", "draining", "drained", "disabled", "offline", "revoked"} {
		status, draining, age := state, 0, 0
		switch state {
		case "draining", "drained":
			status, draining = "active", 1
		case "offline":
			status, age = "active", 600
		}
		pin := fmt.Sprintf("%064d", i)
		code := sha256.Sum256([]byte(state))
		if _, err := h.repo.DB.Exec(`INSERT INTO agents
			(id,name,endpoint,status,certificate_pem,certificate_fingerprint,
			 encrypted_identity,last_heartbeat_at,draining,agent_protocol,agent_version)
			VALUES (?,?, 'https://fixture.invalid',?,'fixture',?,'fixture',?,?,
			 'agent/2',?)`, state, "Fleet "+state, status, pin,
			time.Now().Unix()-int64(age), draining,
			"v0.0.0-20261002120000-123456789012"); err != nil {
			t.Fatal(err)
		}
		if _, err := h.repo.DB.Exec(`INSERT INTO agent_pairings
			(agent_id,pairing_code_hash,agent_public_identity,agent_pin,
			 server_public_identity,server_pin,encrypted_identity,state,
			 expires_at,paired_at) VALUES (?,?,'fixture',?,'fixture',?,
			 'fixture','paired',0,1)`, state, code[:], pin, pin); err != nil {
			t.Fatal(err)
		}
	}
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	issued := seedDeployment(t, h.repo, release.ID, environment.ID, "running")
	if _, err := h.repo.DB.Exec(`INSERT INTO remote_deployment_claims
		(deployment_id,agent_id,state,claim_token_hash,ciphertext,
		 claim_expires_at,last_heartbeat_at,started_at)
		VALUES (?,'draining','started',zeroblob(32),'fixture',
		 unixepoch()+60,unixepoch(),unixepoch())`, issued.ID); err != nil {
		t.Fatal(err)
	}
	user := seedAPIUser(t, h.repo, "fleet-admin@example.test", "admin")
	viewer := seedAPIUser(t, h.repo, "fleet-viewer@example.test", "viewer")
	for _, identity := range []*db.User{user, viewer} {
		if _, err := h.repo.Queries.CreateSession(
			t.Context(),
			db.CreateSessionParams{
				ID: fmt.Sprint(
					identity.ID,
				),
				UserID:    identity.ID,
				CsrfToken: "fleet-csrf",
				ExpiresAt: time.Now().Add(time.Hour).Unix(),
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(
		server.NewRouterWithAgentManagement(h.repo, h.runner,
			cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
			handler.NewAuthHandler(h.repo), fleetBrowserPairing{}, false),
	)
	t.Cleanup(srv.Close)
	browser := startPackageBrowser(t)
	fleetBrowserSession(t, browser, srv.URL, user.ID)
	fleetBrowserNavigate(t, browser, srv.URL+"/admin/agents")
	browser.wait(
		t,
		`document.body.innerText.includes('offline') && document.body.innerText.includes('disabled') && document.body.innerText.includes('revoked') && document.body.innerText.includes('drained')`,
	)

	// When: native Drain and Resume forms are submitted from the fleet list.
	browser.wire.events = nil
	browser.evaluate(
		t,
		`document.querySelector('form[action="/admin/agents/active/drain"] button').click(); true`,
	)
	fleetBrowserLoaded(t, browser)
	browser.wait(
		t,
		`location.pathname === '/admin/agents' && document.querySelector('form[action="/admin/agents/active/resume"]') !== null`,
	)
	agent, err := h.repo.Queries.GetAgent(t.Context(), "active")
	if err != nil || agent.Draining != 1 {
		t.Fatalf("drain agent=%+v err=%v", agent, err)
	}
	browser.screenshot(t, "fleet-drained")
	browser.wire.events = nil
	browser.evaluate(
		t,
		`document.querySelector('form[action="/admin/agents/active/resume"] button').click(); true`,
	)
	fleetBrowserLoaded(t, browser)
	browser.wait(
		t,
		`location.pathname === '/admin/agents' && document.querySelector('form[action="/admin/agents/active/drain"]') !== null`,
	)
	agent, err = h.repo.Queries.GetAgent(t.Context(), "active")
	if err != nil || agent.Draining != 0 {
		t.Fatalf("resume agent=%+v err=%v", agent, err)
	}
	// Detail-page maintenance keeps the detail page open.
	fleetBrowserNavigate(t, browser, srv.URL+"/admin/agents/active")
	for _, action := range []string{"drain", "resume"} {
		browser.wire.events = nil
		browser.evaluate(
			t,
			fmt.Sprintf(
				`document.querySelector('form[action="/admin/agents/active/%s"] button').click(); true`,
				action,
			),
		)
		fleetBrowserLoaded(t, browser)
		browser.wait(t, `location.pathname === '/admin/agents/active'`)
	}
	// Then: health details and fleet states remain readable at every breakpoint.
	for _, width := range []int{375, 768, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width":             width,
			"height":            900,
			"deviceScaleFactor": 1,
			"mobile":            false,
		}, &struct{}{})
		for _, page := range []struct {
			name, path string
			back       bool
		}{
			{"list", "/admin/agents", false},
			{"detail", "/admin/agents/active", true},
			{"draining", "/admin/agents/draining", true},
			{"drained", "/admin/agents/drained", true},
			{"revoked", "/admin/agents/revoked", true},
			{"pairing", "/admin/agents/pair/fixture", true},
			{"reference", "/admin/notifications/settings", true},
		} {
			previous := browser.evaluate(t, `location.pathname`)
			fleetBrowserNavigate(t, browser, srv.URL+page.path)
			if page.name == "draining" {
				browser.wait(
					t,
					`document.body.innerText.includes('Draining: issued work can finish')`,
				)
			}
			if string(
				browser.evaluate(
					t,
					`document.documentElement.scrollWidth > innerWidth`,
				),
			) != "false" {
				t.Fatalf("fleet page %s overflows at %d", page.path, width)
			}
			browser.screenshot(
				t,
				fmt.Sprintf("fleet-%d-%s", width, page.name),
			)
			if page.back {
				browser.wire.events = nil
				browser.evaluate(t, `Array.from(document.querySelectorAll('a'))
					.find(a => a.textContent.trim() === 'Back').click(); true`)
				browser.wait(
					t,
					fmt.Sprintf(
						`location.pathname === %s && document.readyState === 'complete' && window.Alpine && document.querySelector('h1')`,
						previous,
					),
				)
			}
		}
	}
	fleetBrowserNavigate(t, browser, srv.URL+"/admin/agents/active")
	browser.wait(
		t,
		`document.body.innerText.includes('No current work') && document.body.innerText.includes('Compatibility') && document.body.innerText.includes('version unverified')`,
	)
	// An adjacent security regression: viewers cannot open the admin pages.
	fleetBrowserSession(t, browser, srv.URL, viewer.ID)
	fleetBrowserNavigate(t, browser, srv.URL+"/admin/agents")
	browser.wait(
		t,
		`!document.querySelector('form[action$="/drain"]') && document.body.innerText.includes('Unauthorized')`,
	)
	browser.screenshot(t, "fleet-viewer-forbidden")
}

// Only the read-only confirmation page is used; other pairing calls must fail.
type fleetBrowserPairing struct{ agentserver.PairingManager }

func (fleetBrowserPairing) Challenge(
	id string,
) (agentserver.PairingChallenge, error) {
	if id != "fixture" {
		return agentserver.PairingChallenge{}, agentserver.ErrPairingChallenge
	}
	return agentserver.PairingChallenge{
		ID: id, Name: "Fleet pairing", Address: "https://fixture.invalid",
		Fingerprint: fmt.Sprintf("%064d", 1),
	}, nil
}

func fleetBrowserSession(
	t *testing.T,
	browser *packageBrowser,
	origin string,
	userID int64,
) {
	t.Helper()
	var result struct {
		Success bool `json:"success"`
	}
	browser.call(t, "Network.setCookie", map[string]any{
		"name":     "session",
		"value":    fmt.Sprint(userID),
		"url":      origin,
		"httpOnly": true,
	}, &result)
	if !result.Success {
		t.Fatal("fleet browser session was not established")
	}
}

func fleetBrowserNavigate(
	t *testing.T,
	browser *packageBrowser,
	target string,
) {
	t.Helper()
	browser.wire.events = nil
	browser.call(
		t,
		"Page.navigate",
		map[string]string{"url": target},
		&struct{}{},
	)
	fleetBrowserLoaded(t, browser)
	browser.wait(
		t,
		fmt.Sprintf(
			`location.href === %q && document.readyState === 'complete'`,
			target,
		),
	)
}

func fleetBrowserLoaded(t *testing.T, browser *packageBrowser) {
	t.Helper()
	if err := browser.wire.waitEvent(
		"Page.frameStoppedLoading",
		browser.session,
	); err != nil {
		t.Fatal(err)
	}
}
