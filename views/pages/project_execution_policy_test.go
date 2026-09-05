package pages

import (
	"bytes"
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

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

func TestProjectForm_LifecycleHelperUsesResponsiveFlow(t *testing.T) {
	markup := renderProjectExecutionPolicy(
		t,
		context.Background(),
		ProjectForm(
			db.Project{
				Name: "mobile-safe",
				Description: sql.NullString{
					String: "preserved description",
					Valid:  true,
				},
				LifecycleID: sql.NullInt64{Int64: 1, Valid: true},
			},
			false,
			"Choose a default",
			[]db.Lifecycle{{ID: 1, Name: "Approval"}},
			ProjectExecutionPolicyView{
				TargetMode: "label",
				LabelID:    1,
			},
			[]db.AgentLabel{{ID: 1, Name: "Cat Facts"}},
			nil,
			nil,
			false,
		),
	)
	document, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("parse rendered project form: %v", err)
	}
	lifecycleLabel := lifecycleHelpLabel(document)
	if lifecycleLabel == nil {
		t.Fatal("rendered project form has no lifecycle help label")
	}
	children := elementChildren(lifecycleLabel)
	if len(children) != 2 || nodeText(children[0]) != "Lifecycle" ||
		nodeText(
			children[1],
		) != "Optional. Gates deployments to environments in order." {
		t.Fatalf("lifecycle label/help flow = %q", nodeText(lifecycleLabel))
	}
	classes := attribute(lifecycleLabel, "class")
	for _, layout := range []string{
		"flex", "flex-col", "gap-1", "sm:flex-row", "sm:items-center",
	} {
		if !classToken(classes, layout) {
			t.Errorf(
				"lifecycle label misses responsive flow %q: %q",
				layout,
				classes,
			)
		}
	}
	if !strings.Contains(markup, `>preserved description</textarea>`) ||
		!strings.Contains(markup, `value="1" selected`) ||
		!strings.Contains(markup, `value="1" selected>Cat Facts`) {
		t.Fatal(
			"responsive lifecycle layout lost submitted lifecycle or policy form state",
		)
	}
}

func lifecycleHelpLabel(node *html.Node) *html.Node {
	if node.Type == html.ElementNode && node.Data == "label" &&
		strings.Contains(nodeText(node), "Optional. Gates deployments") {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if label := lifecycleHelpLabel(child); label != nil {
			return label
		}
	}
	return nil
}

func elementChildren(node *html.Node) []*html.Node {
	var children []*html.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			children = append(children, child)
		}
	}
	return children
}

func nodeText(node *html.Node) string {
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			text.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(text.String()), " ")
}

func attribute(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func classToken(classes, token string) bool {
	for _, class := range strings.Fields(classes) {
		if class == token {
			return true
		}
	}
	return false
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
