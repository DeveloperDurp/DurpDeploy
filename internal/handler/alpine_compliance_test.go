package handler_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	ordinaryHandler = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	htmxHandler     = regexp.MustCompile(`(?i)\shx-on(?::|-)`)
	xDataAttribute  = regexp.MustCompile(`(?s)x-data\s*=\s*"([^"]*)"`)
	inlineFunction  = regexp.MustCompile(`\b[A-Za-z_$][\w$]*\s*\([^)]*\)\s*\{`)
)

func TestAlpineCompliancePolicy(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")

	t.Run("rejects forbidden fixture categories", func(t *testing.T) {
		fixtures := []struct {
			name    string
			path    string
			content string
			want    string
		}{
			{"inline handler", "views/pages/bad.templ", `<button onclick="save()">`, "ordinary inline event handler"},
			{"htmx handler", "views/pages/bad.templ", `<div hx-on::after-request="save()">`, "inline HTMX event handler"},
			{"page script", "views/pages/bad.templ", `<script>save()</script>`, "script tags = 1, want 0"},
			{"multiline state", "views/pages/bad.templ", "<div x-data=\"{\nopen: false\n}\">", "multiline, oversized, or function-bearing x-data"},
			{"function state", "views/pages/bad.templ", `<div x-data="{ save() { return true } }">`, "multiline, oversized, or function-bearing x-data"},
			{"oversized state", "views/pages/bad.templ", `<div x-data="{ value: '` + strings.Repeat("x", 121) + `' }">`, "multiline, oversized, or function-bearing x-data"},
			{"duplicate bundle", "views/layouts/base.templ", strings.Repeat(`<script src="/static/js/app.bundle.js"></script>`, 2), "duplicate application bundle"},
		}
		for _, fixture := range fixtures {
			t.Run(fixture.name, func(t *testing.T) {
				violations := alpinePolicyViolations(fixture.path, fixture.content)
				if !containsViolation(violations, fixture.want) {
					t.Fatalf("violations = %q, want %q", violations, fixture.want)
				}
			})
		}
	})

	t.Run("production templates comply", func(t *testing.T) {
		matches, err := filepath.Glob(filepath.Join(repositoryRoot, "views", "*", "*.templ"))
		if err != nil {
			t.Fatalf("glob production templates: %v", err)
		}
		for _, path := range matches {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			relative, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				t.Fatalf("resolve %s: %v", path, err)
			}
			for _, violation := range alpinePolicyViolations(relative, string(content)) {
				t.Errorf("%s: %s", relative, violation)
			}
		}
	})

	t.Run("registry and generated assets comply", func(t *testing.T) {
		app := readComplianceFile(t, repositoryRoot, "static/js/app.js")
		order := []string{
			"toast", "navbar", "deploymentForm", "releaseDeployRow",
			"stepFormHost", "stepEditor", "variablesPage", "deploymentStream",
		}
		previous := strings.Index(app, "window.htmx = htmx")
		for _, name := range order {
			marker := "Alpine.data('" + name + "'"
			position := strings.Index(app, marker)
			if strings.Count(app, marker) != 1 || position <= previous {
				t.Errorf("Alpine registry order/count is invalid at %s", name)
			}
			previous = position
		}
		if strings.Count(app, "Alpine.start()") != 1 ||
			strings.LastIndex(app, "Alpine.start()") <= previous {
			t.Error("Alpine.start() must occur once after all registrations")
		}
		if regexp.MustCompile(`console\.(?:log|debug|info)\s*\(`).MatchString(app) {
			t.Error("production app contains console logging")
		}
		if _, err := os.Stat(filepath.Join(repositoryRoot, "static/js/alpine.bundle.js")); !os.IsNotExist(err) {
			t.Errorf("orphan Alpine bundle exists or cannot be checked: %v", err)
		}
		assertContains(t, readComplianceFile(t, repositoryRoot, "static/css/input.css"),
			`[x-cloak] { display: none !important; }`)
	})

	t.Run("target and swap ledger is preserved", func(t *testing.T) {
		contracts := []struct {
			path    string
			markers []string
		}{
			{"views/components/step_form.templ", []string{`hx-target="#step-list"`, `hx-swap="innerHTML"`}},
			{"views/components/step_list.templ", []string{`#step-row-%d`, `hx-swap="outerHTML"`, `hx-target="#step-list"`}},
			{"views/components/template_picker.templ", []string{`hx-target="#step-list"`, `hx-swap="innerHTML"`}},
			{"views/pages/steps.templ", []string{`hx-target="#add-step-form"`, `hx-swap="innerHTML"`}},
			{"views/pages/variables.templ", []string{`#variable-row-%d`, `hx-target="#variables-content"`, `hx-swap="outerHTML"`}},
			{"views/pages/scheduled_deployments.templ", []string{`hx-target="#schedules-content"`, `hx-swap="outerHTML"`}},
			{"views/pages/releases.templ", []string{`hx-target="#releases-content"`, `hx-swap="outerHTML"`}},
			{"views/pages/lifecycle_detail.templ", []string{`hx-target="closest tr"`, `hx-target="#lifecycle-stages"`, `hx-swap="none"`}},
			{"views/pages/deployments.templ", []string{`hx-target="#status-badge"`, `hx-target="#deployments-tbody"`, `hx-swap="beforeend"`}},
		}
		for _, contract := range contracts {
			content := readComplianceFile(t, repositoryRoot, contract.path)
			for _, marker := range contract.markers {
				assertContains(t, content, marker)
			}
		}
	})
}

func containsViolation(violations []string, want string) bool {
	for _, violation := range violations {
		if violation == want {
			return true
		}
	}
	return false
}

func alpinePolicyViolations(path, content string) []string {
	var violations []string
	if ordinaryHandler.MatchString(content) {
		violations = append(violations, "ordinary inline event handler")
	}
	if htmxHandler.MatchString(content) {
		violations = append(violations, "inline HTMX event handler")
	}
	if strings.Count(content, `/static/js/app.bundle.js`) > 1 {
		violations = append(violations, "duplicate application bundle")
	}
	scriptCount := strings.Count(strings.ToLower(content), "<script")
	allowedScripts := 0
	if filepath.ToSlash(path) == "views/layouts/base.templ" {
		allowedScripts = 3
	}
	if scriptCount != allowedScripts {
		violations = append(violations, fmt.Sprintf("script tags = %d, want %d", scriptCount, allowedScripts))
	}
	for _, match := range xDataAttribute.FindAllStringSubmatch(content, -1) {
		state := match[1]
		if strings.ContainsAny(state, "\r\n") || len(state) > 120 || inlineFunction.MatchString(state) {
			violations = append(violations, "multiline, oversized, or function-bearing x-data")
		}
	}
	return violations
}

func readComplianceFile(t *testing.T, root, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func assertContains(t *testing.T, content, marker string) {
	t.Helper()
	if !strings.Contains(content, marker) {
		t.Errorf("missing required contract %q", marker)
	}
}
