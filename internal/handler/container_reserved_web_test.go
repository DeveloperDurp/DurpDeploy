package handler_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestTemplateWebRejectsContainerReservedVariableName(t *testing.T) {
	h := newProjectHarness(t)
	form := url.Values{
		"name":            {"local-template"},
		"script_body":     {"echo hi"},
		"interpreter":     {"bash"},
		"container_image": {"alpine:3.20"},
		"variable_names":  {"DOCKER_HOST"},
		"csrf_token":      {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(h.server.URL+"/templates", form)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422", response.StatusCode)
	}
	if body := readBody(
		t,
		response,
	); !strings.Contains(
		body,
		"Variable name is reserved",
	) {
		t.Fatalf("missing reserved-name error: %s", body)
	}
	templates, err := h.repo.Queries.ListStepTemplates(t.Context())
	if err != nil || len(templates) != 0 {
		t.Fatalf("templates=%+v error=%v", templates, err)
	}
}
