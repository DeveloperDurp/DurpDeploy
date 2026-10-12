//go:build e2e

package api_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func assertDashboardRows(
	t *testing.T,
	document *html.Node,
	wanted map[string]map[int64]string,
) {
	t.Helper()
	for sectionID, expected := range wanted {
		sections := dashboardElements(document, "id", sectionID)
		if len(sections) != 1 {
			t.Fatalf("expected one section %s", sectionID)
		}
		actual := make(map[int64]string)
		for _, node := range dashboardElements(sections[0], "href", "") {
			for _, attr := range node.Attr {
				if attr.Key != "href" {
					continue
				}
				id, err := strconv.ParseInt(
					strings.TrimPrefix(attr.Val, "/deployments/"),
					10,
					64,
				)
				if err != nil {
					t.Fatalf("invalid deployment link %q", attr.Val)
				}
				badges := dashboardElements(
					node.Parent.Parent,
					"class",
					"badge",
				)
				if _, duplicate := actual[id]; duplicate {
					t.Fatalf(
						"duplicate deployment %d in section %s",
						id,
						sectionID,
					)
				}
				actual[id] = dashboardCount(t, badges)
			}
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf(
				"section %s rows/statuses=%v, want %v",
				sectionID,
				actual,
				expected,
			)
		}
	}
}

func dashboardElements(node *html.Node, key, value string) []*html.Node {
	var result []*html.Node
	for _, attr := range node.Attr {
		if attr.Key == key &&
			(value == "" || strings.Contains(" "+attr.Val+" ", " "+value+" ")) {
			result = append(result, node)
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		result = append(result, dashboardElements(child, key, value)...)
	}
	return result
}

func dashboardText(node *html.Node) string {
	if node.Type == html.TextNode {
		return node.Data
	}
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(dashboardText(child))
	}
	return text.String()
}

func dashboardDocument(t *testing.T, body string) *html.Node {
	t.Helper()
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func dashboardCount(t *testing.T, nodes []*html.Node) string {
	t.Helper()
	if len(nodes) != 1 {
		t.Fatalf("expected one dashboard count, got %d", len(nodes))
	}
	return strings.TrimSpace(dashboardText(nodes[0]))
}
