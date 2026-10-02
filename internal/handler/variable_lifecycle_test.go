package handler_test

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestVariableWeb_LifecycleEnvironmentOptions(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("variable-options")
	allowed := h.makeEnv("allowed-variable-env")
	outside := h.makeEnv("outside-variable-env")
	lifecycle := h.makeLifecycle("variable-options-lifecycle", allowed.ID)
	h.assignLifecycle(project.ID, lifecycle.ID)
	path := fmt.Sprintf("%s/projects/%d/variables", h.server.URL, project.ID)
	selectOptions := regexp.MustCompile(
		`(?s)<select name="environment_id"[^>]*>(.*?)</select>`,
	)

	checkOptions := func(path string, outsideAllowed bool) string {
		t.Helper()
		resp, err := h.authedClient().Get(path)
		if err != nil {
			t.Fatalf("get variable form: %v", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read variable form: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("get form status = %d", resp.StatusCode)
		}
		options := selectOptions.FindStringSubmatch(string(body))
		if len(options) != 2 || !strings.Contains(options[1], "Unscoped") ||
			!strings.Contains(options[1], allowed.Name) ||
			strings.Contains(options[1], outside.Name) != outsideAllowed {
			t.Fatalf("unexpected environment options: %q", options)
		}
		return options[1]
	}

	checkOptions(path, false)
	variable, err := h.repo.CreateVariable(t.Context(), db.CreateVariableParams{
		ProjectID: project.ID, Name: "existing-outside",
		EnvironmentID: sql.NullInt64{Int64: outside.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("create existing variable: %v", err)
	}
	editOptions := checkOptions(
		fmt.Sprintf("%s/%d/edit", path, variable.ID),
		false,
	)
	if !strings.Contains(
		editOptions,
		fmt.Sprintf(`value="%d" selected hidden`, outside.ID),
	) {
		t.Fatalf(
			"existing scope must stay selected until explicitly changed: %s",
			editOptions,
		)
	}
	resp, err := h.authedClient().Get(path)
	if err != nil {
		t.Fatalf("get variables: %v", err)
	}
	defer resp.Body.Close()
	markup, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read variables: %v", err)
	}
	if !strings.Contains(string(markup), outside.Name) {
		t.Fatal("existing out-of-lifecycle variable lost its environment label")
	}

	unbound := h.makeProject("unbound-variable-options")
	checkOptions(
		fmt.Sprintf("%s/projects/%d/variables", h.server.URL, unbound.ID),
		true,
	)
}

func TestVariableWeb_RejectsOutsideLifecycleOnCreateAndUpdate(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("variable-write-policy")
	allowed := h.makeEnv("variable-write-allowed")
	outside := h.makeEnv("variable-write-outside")
	lifecycle := h.makeLifecycle("variable-write-lifecycle", allowed.ID)
	h.assignLifecycle(project.ID, lifecycle.ID)
	path := fmt.Sprintf("%s/projects/%d/variables", h.server.URL, project.ID)
	write := func(method, path string, scope int64) int {
		t.Helper()
		environmentID := ""
		if scope != 0 {
			environmentID = fmt.Sprint(scope)
		}
		form := url.Values{
			"name": {"TEST"}, "value": {"value"},
			"environment_id": {environmentID},
			"csrf_token":     {h.csrfToken()},
		}
		req, err := http.NewRequest(
			method,
			path,
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		resp, err := h.authedClient().Do(req)
		if err != nil {
			t.Fatalf("write variable: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if status := write(
		http.MethodPost,
		path,
		outside.ID,
	); status != http.StatusUnprocessableEntity {
		t.Fatalf("outside create = %d", status)
	}
	if status := write(
		http.MethodPost,
		path,
		allowed.ID,
	); status != http.StatusOK {
		t.Fatalf("allowed create = %d", status)
	}
	vars, err := h.repo.Queries.ListVariablesByProject(t.Context(), project.ID)
	if err != nil || len(vars) != 1 {
		t.Fatalf("created variables = %v, %v", vars, err)
	}
	editPath := fmt.Sprintf("%s/%d", path, vars[0].ID)
	if status := write(
		http.MethodPut,
		editPath,
		outside.ID,
	); status != http.StatusUnprocessableEntity {
		t.Fatalf("outside update = %d", status)
	}
	if status := write(http.MethodPut, editPath, 0); status != http.StatusOK {
		t.Fatalf("unscoped update = %d", status)
	}
	if status := write(
		http.MethodPut,
		editPath,
		allowed.ID,
	); status != http.StatusOK {
		t.Fatalf("allowed update = %d", status)
	}
	unbound := h.makeProject("unbound-variable-write")
	if status := write(http.MethodPost,
		fmt.Sprintf("%s/projects/%d/variables", h.server.URL, unbound.ID),
		outside.ID); status != http.StatusOK {
		t.Fatalf("unbound create = %d", status)
	}
	if status := write(http.MethodPost, path, 0); status != http.StatusOK {
		t.Fatalf("unscoped create = %d", status)
	}
}
