package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/handler"
)

func TestLintScript_InvalidJSON(t *testing.T) {
	h := handler.NewLintHandler()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/lint",
		strings.NewReader("not json"),
	)
	rec := httptest.NewRecorder()
	h.LintScript(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestLintScript_ValidScript_NoDiagnostics(t *testing.T) {
	h := handler.NewLintHandler()

	body, _ := json.Marshal(handler.LintRequest{
		Script: "#!/bin/bash\necho \"hello\"\n",
	})
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/lint",
		bytes.NewReader(body),
	)
	rec := httptest.NewRecorder()
	h.LintScript(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp handler.LintResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Diagnostics) != 0 {
		t.Fatalf(
			"expected no diagnostics for a clean script, got %+v",
			resp.Diagnostics,
		)
	}
}

func TestLintScript_ScriptWithIssues_ReturnsDiagnostics(t *testing.T) {
	h := handler.NewLintHandler()

	body, _ := json.Marshal(handler.LintRequest{
		Script: "echo $FOO\n",
	})
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/lint",
		bytes.NewReader(body),
	)
	rec := httptest.NewRecorder()
	h.LintScript(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp handler.LintResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Diagnostics) == 0 {
		t.Fatal(
			"expected diagnostics for a script with an unquoted variable and no shebang",
		)
	}
	for _, d := range resp.Diagnostics {
		if d.Line == 0 {
			t.Errorf("expected non-zero line number, got %+v", d)
		}
		if d.Level == "" {
			t.Errorf("expected non-empty level, got %+v", d)
		}
		if d.Message == "" {
			t.Errorf("expected non-empty message, got %+v", d)
		}
	}
}
