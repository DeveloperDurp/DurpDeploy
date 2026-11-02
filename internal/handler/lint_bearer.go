package handler

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	BearerLintProfile        = "builtin-shell-warnings-v1"
	MaxBearerLintScriptBytes = 65_536
	MaxBearerLintDiagnostics = 100
)

var ErrBearerLintInput = errors.New("invalid lint script")

// LintBearerWarnings returns bounded regex warnings without evaluating source.
// Browser lint keeps its existing messages and unlimited diagnostic semantics.
func LintBearerWarnings(
	ctx context.Context, script string,
) ([]LintDiagnostic, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if len(script) > MaxBearerLintScriptBytes || !utf8.ValidString(script) ||
		strings.ContainsRune(script, 0) {
		return nil, false, ErrBearerLintInput
	}
	diagnostics := make([]LintDiagnostic, 0)
	if !strings.HasPrefix(script, "#!") {
		diagnostics = append(diagnostics, LintDiagnostic{
			Line: 1, Level: "warning", Code: 1000,
			Message: "Script is missing a shebang line",
		})
	}
	for lineNumber := 1; ; lineNumber++ {
		line, rest, more := strings.Cut(script, "\n")
		var truncated bool
		var err error
		diagnostics, truncated, err = lintBearerLine(
			ctx, line, lineNumber, diagnostics,
		)
		if err != nil || truncated {
			return diagnostics, truncated, err
		}
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if !more {
			return diagnostics, false, nil
		}
		script = rest
	}
}

func lintBearerLine(
	ctx context.Context, line string, number int, diagnostics []LintDiagnostic,
) ([]LintDiagnostic, bool, error) {
	for len(line) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		match := unquotedVar.FindStringIndex(line)
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if match == nil {
			break
		}
		if len(diagnostics) == MaxBearerLintDiagnostics {
			return diagnostics, true, nil
		}
		diagnostics = append(diagnostics, LintDiagnostic{
			Line: number, Level: "warning", Code: 2086,
			Message: "Variable reference matched the built-in warning rule",
		})
		line = line[match[1]:]
	}
	return diagnostics, false, nil
}
