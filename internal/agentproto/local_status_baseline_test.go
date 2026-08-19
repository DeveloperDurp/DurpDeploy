package agentproto

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestBaseline_LocalDeploymentStatusVocabulary(t *testing.T) {
	// Given: the local deployment's persisted default and runner updates.
	migration, err := os.ReadFile("../../migrations/001_schema.sql")
	if err != nil {
		t.Fatalf("read deployment schema: %v", err)
	}
	if !strings.Contains(
		string(migration),
		"status TEXT NOT NULL DEFAULT 'pending'",
	) {
		t.Fatal("deployment schema no longer defaults status to pending")
	}

	statuses := runnerStatusLiterals(t)
	statuses = append(statuses, "pending")
	sort.Strings(statuses)

	// When: the current local state vocabulary is observed.
	// Then: remote-only states have not leaked into the local runner yet.
	want := []string{"cancelled", "failed", "pending", "running", "succeeded"}
	if !slices.Equal(statuses, want) {
		t.Fatalf("local deployment statuses = %v, want %v", statuses, want)
	}
}

func runnerStatusLiterals(t *testing.T) []string {
	t.Helper()

	var statuses []string
	for _, source := range []string{
		"../runner/runner.go",
		"../runner/deployment_runner.go",
	} {
		file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
		if err != nil {
			t.Fatalf("parse runner source %q: %v", source, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isUpdateDeploymentStatusParams(literal) {
				return true
			}
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok || fieldName(field.Key) != "Status" {
					continue
				}
				value, ok := field.Value.(*ast.BasicLit)
				if ok && value.Kind == token.STRING {
					statuses = append(
						statuses,
						value.Value[1:len(value.Value)-1],
					)
				}
			}
			return true
		})
	}

	sort.Strings(statuses)
	return slices.Compact(statuses)
}

func isUpdateDeploymentStatusParams(literal *ast.CompositeLit) bool {
	selector, ok := literal.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return selector.Sel.Name == "UpdateDeploymentStatusParams"
}

func fieldName(expression ast.Expr) string {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}
