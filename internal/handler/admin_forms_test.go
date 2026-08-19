package handler_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAgentAdmin_HTMLFormShowsEnrollmentTokenOnlyOnce(t *testing.T) {
	// Given
	h := newHarness(t)
	newAgent, err := h.sess.client.Get(h.server.URL + "/admin/agents/new")
	if err != nil {
		t.Fatalf("GET new agent form: %v", err)
	}
	defer newAgent.Body.Close()
	if newAgent.StatusCode != http.StatusOK {
		t.Fatalf(
			"GET new agent form: status = %d, want %d",
			newAgent.StatusCode,
			http.StatusOK,
		)
	}

	// When
	createValues := url.Values{
		"agent_version": {"test"},
		"csrf_token":    {h.sess.csrfToken},
		"id":            {"browser-agent"},
		"name":          {"Browser Agent"},
	}
	create, err := http.NewRequest(
		http.MethodPost,
		h.server.URL+"/admin/agents",
		strings.NewReader(createValues.Encode()),
	)
	if err != nil {
		t.Fatalf("create agent request: %v", err)
	}
	create.Header.Set("Accept", "text/html")
	create.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	created, err := h.sess.client.Do(create)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer created.Body.Close()
	if created.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"create agent: status = %d, want %d",
			created.StatusCode,
			http.StatusSeeOther,
		)
	}
	if created.Header.Get("Location") != "/admin/agents/browser-agent" {
		t.Fatalf("create agent: location = %q", created.Header.Get("Location"))
	}

	enrollmentPage, err := h.sess.client.Get(
		h.server.URL + "/admin/agents/browser-agent/enrollment",
	)
	if err != nil {
		t.Fatalf("GET enrollment form: %v", err)
	}
	defer enrollmentPage.Body.Close()
	pageBody, err := io.ReadAll(enrollmentPage.Body)
	if err != nil {
		t.Fatalf("read enrollment form: %v", err)
	}
	if enrollmentPage.StatusCode != http.StatusOK ||
		!strings.Contains(string(pageBody), "Generate enrollment token") ||
		strings.Contains(string(pageBody), "ddp_enroll_") {
		t.Fatalf(
			"GET enrollment form did not render a token-free confirmation page",
		)
	}

	enrollValues := url.Values{"csrf_token": {h.sess.csrfToken}}
	enroll, err := http.NewRequest(
		http.MethodPost,
		h.server.URL+"/admin/agents/browser-agent/enrollment",
		strings.NewReader(enrollValues.Encode()),
	)
	if err != nil {
		t.Fatalf("create enrollment request: %v", err)
	}
	enroll.Header.Set("Accept", "text/html")
	enroll.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	enrolled, err := h.sess.client.Do(enroll)
	if err != nil {
		t.Fatalf("create enrollment: %v", err)
	}
	defer enrolled.Body.Close()
	tokenBody, err := io.ReadAll(enrolled.Body)
	if err != nil {
		t.Fatalf("read enrollment response: %v", err)
	}

	// Then
	if enrolled.StatusCode != http.StatusCreated ||
		enrolled.Header.Get("Cache-Control") != "no-store" ||
		!strings.Contains(string(tokenBody), "ddp_enroll_") {
		t.Fatal(
			"POST enrollment did not return the one-time no-store token page",
		)
	}
	reload, err := h.sess.client.Get(
		h.server.URL + "/admin/agents/browser-agent/enrollment",
	)
	if err != nil {
		t.Fatalf("reload enrollment form: %v", err)
	}
	defer reload.Body.Close()
	reloadBody, err := io.ReadAll(reload.Body)
	if err != nil {
		t.Fatalf("read reloaded enrollment form: %v", err)
	}
	if strings.Contains(string(reloadBody), "ddp_enroll_") {
		t.Fatal("reloaded enrollment form retained the one-time token")
	}
}

func TestPoolAdmin_HTMLFormCreatesEnabledPool(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	values := url.Values{
		"csrf_token":  {h.sess.csrfToken},
		"description": {"browser pool"},
		"name":        {"browser-pool"},
	}
	request, err := http.NewRequest(
		http.MethodPost,
		h.server.URL+"/admin/pools",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatalf("create pool request: %v", err)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.sess.client.Do(request)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/admin/pools/1" {
		t.Fatalf("create pool did not redirect to the created pool")
	}
}

func TestPoolAdmin_HTMLFormAddsCandidateMember(t *testing.T) {
	// Given
	h := newHarness(t)
	pool := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/pools",
		`{"name":"browser-pool"}`,
		true,
	)
	requireAdminStatus(t, pool, http.StatusCreated)
	agent := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents",
		`{"id":"browser-agent","name":"Browser Agent"}`,
		true,
	)
	requireAdminStatus(t, agent, http.StatusCreated)

	// When
	page, err := h.sess.client.Get(h.server.URL + "/admin/pools/1")
	if err != nil {
		t.Fatalf("GET pool form: %v", err)
	}
	defer page.Body.Close()
	body, err := io.ReadAll(page.Body)
	if err != nil {
		t.Fatalf("read pool form: %v", err)
	}
	values := url.Values{
		"agent_id":   {"browser-agent"},
		"csrf_token": {h.sess.csrfToken},
	}
	request, err := http.NewRequest(
		http.MethodPost,
		h.server.URL+"/admin/pools/1/members",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatalf("create member request: %v", err)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.sess.client.Do(request)
	if err != nil {
		t.Fatalf("add member: %v", err)
	}
	defer response.Body.Close()

	// Then
	if page.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "Add member") {
		t.Fatal("pool page did not render the candidate member form")
	}
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/admin/pools/1" {
		t.Fatalf("add member did not redirect to the pool: %s", response.Status)
	}
}

func TestAgentAdmin_HTMLFormAddsTag(t *testing.T) {
	// Given
	h := newHarness(t)
	agent := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents",
		`{"id":"browser-agent","name":"Browser Agent"}`,
		true,
	)
	requireAdminStatus(t, agent, http.StatusCreated)

	// When
	values := url.Values{
		"csrf_token": {h.sess.csrfToken},
		"key":        {"region"},
		"value":      {"browser"},
	}
	request, err := http.NewRequest(
		http.MethodPost,
		h.server.URL+"/admin/agents/browser-agent/tags",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatalf("create tag request: %v", err)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.sess.client.Do(request)
	if err != nil {
		t.Fatalf("add tag: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/admin/agents/browser-agent" {
		t.Fatalf("add tag did not redirect to the agent: %s", response.Status)
	}
	tags, err := h.repo.Queries.ListAgentTagsByAgent(
		t.Context(),
		"browser-agent",
	)
	if err != nil {
		t.Fatalf("list agent tags: %v", err)
	}
	if len(tags) != 1 || tags[0].TagKey != "region" ||
		tags[0].TagValue != "browser" {
		t.Fatalf("agent tags = %#v", tags)
	}
}
