package handler_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestLifecycle_Get_renders_merged_workspace(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	assigned := h.makeEnv("assigned")
	available := h.makeEnv("available")
	lifecycle := h.makeLifecycle("release flow", assigned.ID)

	// When
	response, err := h.authedClient().Get(
		h.server.URL + "/lifecycles/" + strconv.FormatInt(lifecycle.ID, 10),
	)
	if err != nil {
		t.Fatalf("get lifecycle: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read lifecycle response: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	for _, marker := range []string{
		"release flow",
		"assigned",
		`<option value="` + strconv.FormatInt(available.ID, 10) + `">available</option>`,
		`data-lifecycle-settings`,
		`data-lifecycle-environment-assignment`,
	} {
		if !strings.Contains(string(body), marker) {
			t.Errorf("response missing %q", marker)
		}
	}
}

func TestLifecycle_Edit_redirects_to_merged_workspace(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	lifecycle := h.makeLifecycle("release flow")
	path := "/lifecycles/" + strconv.FormatInt(lifecycle.ID, 10)

	// When
	response, err := h.authedClient().Get(h.server.URL + path + "/edit")
	if err != nil {
		t.Fatalf("get lifecycle edit: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != path {
		t.Errorf("Location = %q, want %q", location, path)
	}
}

func TestLifecycle_Create_redirects_to_lifecycle_list(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	form := url.Values{
		"name":        {"release flow"},
		"description": {"promote through environments"},
		"csrf_token":  {h.csrfToken()},
	}

	// When
	response, err := h.authedClient().PostForm(
		h.server.URL+"/lifecycles",
		form,
	)
	if err != nil {
		t.Fatalf("create lifecycle: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/lifecycles" {
		t.Errorf("Location = %q, want %q", location, "/lifecycles")
	}
	lifecycles, err := h.repo.Queries.ListLifecycles(context.Background())
	if err != nil {
		t.Fatalf("list lifecycles: %v", err)
	}
	if len(lifecycles) != 1 || lifecycles[0].Name != "release flow" {
		t.Errorf("lifecycles = %#v, want created lifecycle", lifecycles)
	}
}

func TestLifecycle_Delete_redirects_to_lifecycle_list(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	lifecycle := h.makeLifecycle("release flow")
	path := "/lifecycles/" + strconv.FormatInt(lifecycle.ID, 10)
	form := url.Values{
		"_method":    {"delete"},
		"csrf_token": {h.csrfToken()},
	}

	// When
	response, err := h.authedClient().PostForm(h.server.URL+path, form)
	if err != nil {
		t.Fatalf("delete lifecycle: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/lifecycles" {
		t.Errorf("Location = %q, want %q", location, "/lifecycles")
	}
	_, err = h.repo.Queries.GetLifecycle(context.Background(), lifecycle.ID)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("get deleted lifecycle error = %v, want sql.ErrNoRows", err)
	}
}

func TestLifecycle_Save_updates_lifecycle(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	lifecycle := h.makeLifecycle("old name")
	path := "/lifecycles/" + strconv.FormatInt(lifecycle.ID, 10)
	form := url.Values{
		"_method":     {"put"},
		"name":        {"new name"},
		"description": {"new description"},
		"csrf_token":  {h.csrfToken()},
	}

	// When
	response, err := h.authedClient().PostForm(h.server.URL+path, form)
	if err != nil {
		t.Fatalf("save lifecycle: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.StatusCode)
	}
	if location := response.Header.Get("Location"); location != "/lifecycles" {
		t.Errorf("Location = %q, want %q", location, "/lifecycles")
	}
	updated, err := h.repo.Queries.GetLifecycle(context.Background(), lifecycle.ID)
	if err != nil {
		t.Fatalf("get updated lifecycle: %v", err)
	}
	if updated.Name != "new name" || updated.Description.String != "new description" {
		t.Errorf("updated lifecycle = %#v", updated)
	}
}

func TestLifecycle_Save_renders_workspace_when_name_missing(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	assigned := h.makeEnv("assigned")
	available := h.makeEnv("available")
	lifecycle := h.makeLifecycle("release flow", assigned.ID)
	path := "/lifecycles/" + strconv.FormatInt(lifecycle.ID, 10)
	form := url.Values{
		"_method":     {"put"},
		"name":        {" "},
		"description": {"keep this description"},
		"csrf_token":  {h.csrfToken()},
	}

	// When
	response, err := h.authedClient().PostForm(h.server.URL+path, form)
	if err != nil {
		t.Fatalf("save lifecycle: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read lifecycle response: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}
	for _, marker := range []string{
		"Name is required",
		"keep this description",
		"assigned",
		`<option value="` + strconv.FormatInt(available.ID, 10) + `">available</option>`,
	} {
		if !strings.Contains(string(body), marker) {
			t.Errorf("response missing %q", marker)
		}
	}
}

func TestLifecycle_Save_renders_workspace_when_name_exists(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	h.makeLifecycle("existing name")
	lifecycle := h.makeLifecycle("release flow")
	path := "/lifecycles/" + strconv.FormatInt(lifecycle.ID, 10)
	form := url.Values{
		"_method":    {"put"},
		"name":       {"existing name"},
		"csrf_token": {h.csrfToken()},
	}

	// When
	response, err := h.authedClient().PostForm(h.server.URL+path, form)
	if err != nil {
		t.Fatalf("save lifecycle: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read lifecycle response: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.StatusCode)
	}
	if !strings.Contains(string(body), "A lifecycle with this name already exists") {
		t.Error("response missing duplicate-name error")
	}
}
