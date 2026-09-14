package handler_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func TestDeploymentDetailsOwnsEventSourceLifecycle(t *testing.T) {
	// Given
	deployment := db.Deployment{ID: 42, Status: "running"}
	var rendered bytes.Buffer

	// When
	err := pages.DeploymentDetail(
		db.Project{Name: "stream-project"},
		db.Release{Version: "1.0.0", StepsJson: "[]"},
		db.Environment{Name: "stream-environment"},
		deployment,
		nil,
	).Render(context.Background(), &rendered)
	if err != nil {
		t.Fatalf("render deployment detail: %v", err)
	}

	// Then
	body := rendered.String()
	markers := []string{
		`x-data="deploymentStream({ url: &#39;/deployments/42/logs/stream&#39; })"`,
		`x-on:htmx:after-swap.camel.window="status($event)"`,
		`x-ref="noLogs"`,
		`x-ref="logs"`,
		`id="status-badge"`,
		`hx-get="/deployments/42/status"`,
		`hx-trigger="every 3s"`,
		`hx-swap="outerHTML"`,
		`hx-post="/deployments/42/cancel"`,
		`hx-target="#status-badge"`,
	}
	for _, marker := range markers {
		if !strings.Contains(body, marker) {
			t.Errorf("deployment detail missing lifecycle marker %q", marker)
		}
	}
	if got := strings.Count(body, "/deployments/42/logs/stream"); got != 1 {
		t.Errorf("deployment stream URL count = %d, want 1", got)
	}
	if strings.Contains(body, "new EventSource") ||
		strings.Contains(body, "<script") {
		t.Error(
			"deployment detail must not create an EventSource in page-local script",
		)
	}
}
