package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
)

type LintRequest struct {
	Script string `json:"script"`
}

type LintDiagnostic struct {
	Line    int    `json:"line"`
	Level   string `json:"level"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type LintResponse struct {
	Diagnostics []LintDiagnostic `json:"diagnostics"`
}

type LintHandler struct{}

func NewLintHandler() *LintHandler {
	return &LintHandler{}
}

// LintScript runs ShellCheck (via the embedded WASM binary) against the
// posted script body and returns the diagnostics as JSON. This endpoint
// is read-only, so it is exempted from CSRF/viewer checks in
// internal/auth/csrf.go but still requires a valid authenticated session.
func (h *LintHandler) LintScript(w http.ResponseWriter, r *http.Request) {
	var req LintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	diagnostics, err := runShellCheck(r.Context(), req.Script)
	if err != nil {
		http.Error(
			w,
			"failed to run shellcheck: "+err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(LintResponse{Diagnostics: diagnostics})
}

func runShellCheck(
	ctx context.Context,
	script string,
) ([]LintDiagnostic, error) {
	cmd := exec.CommandContext(
		ctx,
		"go",
		"run",
		"github.com/wasilibs/go-shellcheck/cmd/shellcheck",
		"-f",
		"json",
		"-",
	)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return nil, err
		}
	}

	var diagnostics []LintDiagnostic
	if stdout.Len() > 0 {
		if err := json.Unmarshal(stdout.Bytes(), &diagnostics); err != nil {
			return nil, err
		}
	}
	return diagnostics, nil
}
