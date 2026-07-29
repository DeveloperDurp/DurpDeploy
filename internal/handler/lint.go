package handler

import (
	"encoding/json"
	"net/http"
	"regexp"
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

// LintScript runs a lightweight shell linter against the posted script body
// and returns the diagnostics as JSON. This endpoint is read-only, so it is
// exempted from CSRF/viewer checks in internal/auth/csrf.go but still
// requires a valid authenticated session.
func (h *LintHandler) LintScript(w http.ResponseWriter, r *http.Request) {
	var req LintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).
		Encode(LintResponse{Diagnostics: lintScript(req.Script)})
}

var unquotedVar = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*`)

func lintScript(script string) []LintDiagnostic {
	var diagnostics []LintDiagnostic
	lines := strings.Split(script, "\n")
	hasShebang := len(lines) > 0 && strings.HasPrefix(lines[0], "#!")
	if !hasShebang {
		diagnostics = append(diagnostics, LintDiagnostic{
			Line:    1,
			Level:   "warning",
			Code:    1000,
			Message: "Script is missing a shebang line",
		})
	}
	for i, line := range lines {
		for _, m := range unquotedVar.FindAllString(line, -1) {
			diagnostics = append(diagnostics, LintDiagnostic{
				Line:    i + 1,
				Level:   "warning",
				Code:    2086,
				Message: "Unquoted variable: " + m,
			})
		}
	}
	return diagnostics
}
