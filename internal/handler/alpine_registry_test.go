package handler_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestAlpineRegistryDefinesComplianceComponents(t *testing.T) {
	// Given
	sourcePath := filepath.Join("..", "..", "static", "js", "app.js")
	bundlePath := filepath.Join("..", "..", "static", "js", "app.bundle.js")
	sourceBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read Alpine registry source: %v", err)
	}
	bundleBytes, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read Alpine registry bundle: %v", err)
	}
	source := string(sourceBytes)
	bundle := string(bundleBytes)

	contracts := []struct {
		name        string
		constructor string
		members     []string
		methods     []string
		events      []string
		cleanup     []string
	}{
		{
			name:        "deploymentForm",
			constructor: `\(\{ releaseID, environmentID \}\)`,
			members: []string{
				"releaseChanged", "environmentChanged", "forceChanged",
				"submitLabel", "forceVisible",
			},
			methods: []string{
				"releaseChanged", "environmentChanged", "forceChanged",
			},
		},
		{
			name:        "releaseDeployRow",
			constructor: `\(\)`,
			members:     []string{"forceChecked"},
		},
		{
			name:        "stepFormHost",
			constructor: `\(\)`,
			members:     []string{"afterRequest", "add", "cancel", "handleEvent"},
			methods:     []string{"afterRequest", "add", "cancel", "handleEvent"},
			events: []string{
				"step-form-add", "step-form-cancel", "step-form-edit",
			},
		},
		{
			name:        "stepEditor",
			constructor: `\(\)`,
			members: []string{
				"init", "input", "scroll", "fullscreen", "destroy",
			},
			methods: []string{
				"init", "input", "scroll", "fullscreen", "destroy",
			},
			cleanup: []string{"clearTimeout", ".abort()"},
		},
		{
			name:        "variablesPage",
			constructor: `\(\)`,
			members: []string{
				"override", "focusAfterSwap", "init", "destroy",
			},
			methods: []string{
				"override", "focusAfterSwap", "init", "destroy",
			},
			events:  []string{"htmx:afterSwap"},
			cleanup: []string{"removeEventListener"},
		},
		{
			name:        "deploymentStream",
			constructor: `\(\{ url \}\)`,
			members:     []string{"init", "message", "status", "destroy"},
			methods:     []string{"init", "message", "status", "destroy"},
			events:      []string{"htmx:afterSwap"},
			cleanup:     []string{".close()"},
		},
	}

	// When / Then
	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			factoryPattern := regexp.MustCompile(
				`(?ms)Alpine\.data\('` + contract.name + `'\s*,\s*` +
					contract.constructor + `\s*=>\s*\(\{.*?^\}\)\);`,
			)
			factory := factoryPattern.FindString(source)
			if factory == "" {
				t.Fatalf("registry does not define %s with constructor %s", contract.name, contract.constructor)
			}
			for _, member := range contract.members {
				memberPattern := regexp.MustCompile(`(?m)^\t(?:get )?` + member + `(?:\(|:)`)
				if !memberPattern.MatchString(factory) {
					t.Errorf("%s does not expose %s", contract.name, member)
				}
			}
			methodPattern := regexp.MustCompile(
				`(?m)^\t([A-Za-z][A-Za-z0-9]*)\([^\n]*\) \{`,
			)
			matches := methodPattern.FindAllStringSubmatch(factory, -1)
			methods := make([]string, 0, len(matches))
			for _, match := range matches {
				methods = append(methods, match[1])
			}
			if !slices.Equal(methods, contract.methods) {
				t.Errorf(
					"%s methods = %v, want exactly %v",
					contract.name,
					methods,
					contract.methods,
				)
			}
			for _, event := range contract.events {
				if !strings.Contains(factory, `'`+event+`'`) {
					t.Errorf("%s does not own event %q", contract.name, event)
				}
			}
			for _, cleanup := range contract.cleanup {
				if !strings.Contains(factory, cleanup) {
					t.Errorf("%s destroy contract lacks %q", contract.name, cleanup)
				}
			}
			if !strings.Contains(bundle, contract.name) {
				t.Errorf("generated bundle is stale: missing %s", contract.name)
			}
		})
	}

	if strings.Count(source, "Alpine.data('toast'") != 1 ||
		strings.Count(source, "Alpine.data('navbar'") != 1 {
		t.Error("registry must preserve one toast and one navbar factory")
	}
	toastPattern := regexp.MustCompile(
		`(?ms)Alpine\.data\('toast'.*?^\}\)\);`,
	)
	toast := toastPattern.FindString(source)
	if !strings.Contains(toast, "clearTimeout") ||
		!strings.Contains(toast, "removeEventListener") {
		t.Error("toast must clear its timer and global listeners on destroy")
	}
	if strings.Count(source, "Alpine.start()") != 1 ||
		!strings.HasSuffix(strings.TrimSpace(source), "Alpine.start()") {
		t.Error("registry must end with exactly one Alpine.start()")
	}
	if strings.Contains(source, "console.log") ||
		strings.Contains(bundle, "fullAlertClass called") {
		t.Error("production app source must not contain debug console logging")
	}
	registryOrder := []string{
		"toast", "navbar", "deploymentForm", "releaseDeployRow",
		"stepFormHost", "stepEditor", "variablesPage", "deploymentStream",
	}
	previous := strings.Index(source, "window.htmx = htmx")
	for _, name := range registryOrder {
		registration := "Alpine.data('" + name + "'"
		if strings.Count(source, registration) != 1 {
			t.Errorf("registry must define %s exactly once", name)
		}
		position := strings.Index(source, registration)
		if position <= previous {
			t.Errorf("registry order is invalid at %s", name)
		}
		previous = position
	}
	if strings.LastIndex(source, "Alpine.start()") <= previous {
		t.Error("Alpine.start() must follow all registry definitions")
	}

	hostPattern := regexp.MustCompile(
		`(?ms)Alpine\.data\('stepFormHost'.*?^\}\)\);`,
	)
	host := hostPattern.FindString(source)
	destroyEditor := strings.Index(host, "Alpine.destroyTree(form)")
	removeForm := strings.Index(host, "replaceChildren()")
	if destroyEditor < 0 || removeForm < 0 || destroyEditor > removeForm {
		t.Error(
			"stepFormHost must synchronously destroy editors before removing the form",
		)
	}
}
