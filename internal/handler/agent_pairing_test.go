package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/agentbootstrap"
	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
)

func TestAgentPairing_HTMLConfirmation_persistsPairingWithoutRenderingCode(
	t *testing.T,
) {
	// Given
	bootstrap, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: t.TempDir(), ListenAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("start bootstrap agent: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		if shutdownErr := bootstrap.Shutdown(shutdownCtx); shutdownErr != nil {
			t.Errorf("shutdown bootstrap agent: %v", shutdownErr)
		}
	})
	serverIdentity, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://127.0.0.1",
	)
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	pullEndpoint, err := agentproto.ParsePullEndpoint("https://127.0.0.1:10944")
	if err != nil {
		t.Fatalf("parse pull endpoint: %v", err)
	}
	pairer, err := agentpairing.NewServer(pullEndpoint, serverIdentity)
	if err != nil {
		t.Fatalf("create pairer: %v", err)
	}
	conn := newHandlerTestDatabase(t)
	repo := repository.New(conn)
	rnr := runner.New(repo, runner.NewLogBroker())
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	h := httptest.NewServer(server.NewRouterWithAgentPairer(
		repo,
		rnr,
		nil,
		parser,
		handler.NewAuthHandler(repo),
		pairer,
	))
	t.Cleanup(h.Close)
	session := seedSession(t, repo, h.URL, "admin")
	if _, err := repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{
			ID: "paired-agent", Name: "Paired agent",
		},
	); err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	offer, err := agentpairing.FetchBootstrap(
		context.Background(),
		bootstrap.Endpoint(),
	)
	if err != nil {
		t.Fatalf("fetch bootstrap: %v", err)
	}
	code := pairingCodeText(t, offer.PairingCode)

	// When
	begin := session.formRequest(
		t,
		h.URL+"/admin/agents/paired-agent/pairings",
		url.Values{
			"agent_endpoint": {bootstrap.Endpoint()},
			"pairing_code":   {code},
			"csrf_token":     {session.csrfToken},
		},
	)
	beginResponse, err := session.client.Do(begin)
	if err != nil {
		t.Fatalf("begin pairing: %v", err)
	}
	defer beginResponse.Body.Close()
	body, err := io.ReadAll(beginResponse.Body)
	if err != nil {
		t.Fatalf("read confirmation: %v", err)
	}
	confirm := session.formRequest(
		t,
		h.URL+"/admin/agents/paired-agent/pairings/confirm",
		url.Values{
			"agent_pin":  {offer.AgentPin.String()},
			"csrf_token": {session.csrfToken},
		},
	)
	confirmResponse, err := session.client.Do(confirm)
	if err != nil {
		t.Fatalf("confirm pairing: %v", err)
	}
	defer confirmResponse.Body.Close()

	// Then
	if beginResponse.StatusCode != http.StatusCreated ||
		beginResponse.Header.Get("Cache-Control") != "no-store" ||
		!strings.Contains(string(body), offer.AgentPin.String()) ||
		strings.Contains(string(body), code) {
		t.Fatal("pairing confirmation response rendered a pairing secret")
	}
	if confirmResponse.StatusCode != http.StatusSeeOther ||
		confirmResponse.Header.Get("Location") != "/admin/agents/paired-agent" {
		t.Fatalf(
			"confirm response = %d %q",
			confirmResponse.StatusCode,
			confirmResponse.Header.Get("Location"),
		)
	}
	paired, err := repo.Queries.GetAgentPairing(
		context.Background(),
		"paired-agent",
	)
	if err != nil || paired.State != "paired" || !paired.ServerPin.Valid {
		t.Fatalf("stored pairing = %#v, %v", paired, err)
	}
	activated, err := repo.Queries.GetAgent(
		context.Background(),
		"paired-agent",
	)
	if err != nil || activated.Status != "active" ||
		activated.CertificateFingerprint.String != offer.AgentPin.String() {
		t.Fatalf("activated paired agent = %#v, %v", activated, err)
	}
	hash := offer.PairingCode.Hash()
	if !bytes.Equal(paired.PairingCodeHash, hash[:]) {
		t.Fatal("stored pairing did not retain only the pairing-code hash")
	}
	if !hasAuditAction(t, &testHarness{repo: repo}, "create_agent_pairing") ||
		!hasAuditAction(t, &testHarness{repo: repo}, "confirm_agent_pairing") {
		t.Fatal("pairing audit actions are missing")
	}
	select {
	case <-bootstrap.Done():
	case <-time.After(time.Second):
		t.Fatal(
			"agent bootstrap listener remained available after confirmation",
		)
	}
}

func (session *authedSession) formRequest(
	t *testing.T,
	target string,
	values url.Values,
) *http.Request {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		target,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatalf("create form request: %v", err)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func pairingCodeText(t *testing.T, code agentproto.PairingCode) string {
	t.Helper()
	encoded, err := json.Marshal(code)
	if err != nil {
		t.Fatalf("encode pairing code: %v", err)
	}
	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("decode pairing code: %v", err)
	}
	return value
}
