package pages

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestProjectExecutionPolicyFields_RenderLegacyAndPreserveInvalidSelection(
	t *testing.T,
) {
	// Given a legacy project and one safe label choice.
	request := auth.SetUser(
		httptest.NewRequest("GET", "/projects/1/edit", nil),
		&db.User{Role: "admin"},
	)
	view := ProjectExecutionPolicyView{
		Legacy:     true,
		TargetMode: "label",
		LabelID:    1,
		Strategy:   "round_robin",
	}

	// When the policy controls are rendered after a validation failure.
	markup := renderProjectExecutionPolicy(
		t,
		request.Context(),
		ProjectExecutionPolicyFields(view, []db.AgentLabel{{
			ID: 1, Name: "Cat Fact", NormalizedName: "cat fact",
		}}),
	)

	// Then legacy behavior is explained and the user's selection survives.
	for _, required := range []string{
		"Legacy execution behavior",
		"environment assignments when present, otherwise local",
		`name="target_mode" value="label" class="radio radio-primary" checked`,
		`name="agent_label_id"`,
		`value="1" selected`,
		`name="agent_strategy" value="round_robin" class="radio radio-sm" checked`,
		"Cat Fact",
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("policy markup is missing %q: %s", required, markup)
		}
	}
	if strings.Contains(markup, "cat fact") {
		t.Fatalf("policy markup leaked normalized label key: %s", markup)
	}
	t.Logf(
		"rendered form state: legacy=true mode=label label_id=1 strategy=round_robin",
	)
}

func TestProjectExecutionPolicySummary_ExplainsLegacyAndLocalStates(
	t *testing.T,
) {
	request := auth.SetUser(
		httptest.NewRequest("GET", "/projects/1", nil),
		&db.User{Role: "admin"},
	)
	legacy := renderProjectExecutionPolicy(
		t,
		request.Context(),
		ProjectExecutionPolicySummary(ProjectExecutionPolicyView{Legacy: true}),
	)
	local := renderProjectExecutionPolicy(
		t,
		request.Context(),
		ProjectExecutionPolicySummary(
			ProjectExecutionPolicyView{TargetMode: "local"},
		),
	)
	if !strings.Contains(legacy, "Uses environment assignments when present") ||
		!strings.Contains(local, "Runs on this DurpDeploy server") {
		t.Fatalf(
			"unexpected project execution summaries: legacy=%q local=%q",
			legacy,
			local,
		)
	}
	t.Log("rendered detail state: legacy warning and local summary present")
}

func renderProjectExecutionPolicy(
	t *testing.T,
	ctx context.Context,
	component templ.Component,
) string {
	t.Helper()
	var output bytes.Buffer
	if err := component.Render(ctx, &output); err != nil {
		t.Fatalf("render project execution policy: %v", err)
	}
	return output.String()
}
