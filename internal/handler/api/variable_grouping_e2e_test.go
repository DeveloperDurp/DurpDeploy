//go:build e2e

package api_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"durpdeploy/internal/config"
	"golang.org/x/net/html"
)

func TestVariableGroupingAPIWebE2E(t *testing.T) {
	f := newConfiguredBodyFixture(t, config.Defaults().Limits.HTTP)
	var dev, prd struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/environments",
		map[string]string{"name": "dev"}, 201), &dev)
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/environments",
		map[string]string{"name": "prd"}, 201), &prd)
	path := f.base() + "/variables"
	create := func(name string, environment any, secret bool) int64 {
		t.Helper()
		var variable struct{ ID int64 }
		decodeStepLogTest(t, f.api(t, "POST", path, map[string]any{
			"name": name, "environment_id": environment,
			"value": "grouping-test-value", "secret": secret,
		}, 201), &variable)
		return variable.ID
	}
	prdURL := create("APP_URL", prd.ID, true)
	other := create("app_url", dev.ID, false)
	devURL := create("APP_URL", dev.ID, false)
	global := create("GLOBAL_URL", nil, false)

	checkAPI := func(query string, want []int64) {
		t.Helper()
		var list struct {
			Items []struct {
				ID    int64
				Value string
			}
		}
		decodeStepLogTest(t, f.api(t, "GET", path+query, nil, 200), &list)
		var got []int64
		for _, item := range list.Items {
			got = append(got, item.ID)
			if item.ID == prdURL && item.Value != "" {
				t.Fatal("grouped list exposed a secret")
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("list %s: got IDs %v, want %v", query, got, want)
		}
	}
	checkAPI("?sort=name", []int64{devURL, prdURL, global, other})
	checkAPI("?sort=name&limit=2&offset=1", []int64{prdURL, global})
	checkAPI(
		fmt.Sprintf("?sort=name&environment_id=%d", dev.ID),
		[]int64{devURL, other},
	)
	f.api(t, "GET", path+"?sort=invalid", nil, 400)

	page := f.web(t, "GET", strings.TrimPrefix(path, "/api/v1"), nil, 200)
	document, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	checkVariableGroup(t, document, "APP_URL", devURL, prdURL)
	checkVariableGroup(t, document, "app_url", other)
	for _, marker := range []string{`id="variable-row-%d"`, `data-mobile-variable="%d"`} {
		remaining := page
		for _, id := range []int64{global, devURL, prdURL, other} {
			match := fmt.Sprintf(marker, id)
			_, after, found := strings.Cut(remaining, match)
			if !found {
				t.Fatalf(
					"variable %d missing or out of order for %s",
					id,
					marker,
				)
			}
			remaining = after
		}
	}
}

func checkVariableGroup(
	t *testing.T,
	document *html.Node,
	name string,
	ids ...int64,
) {
	t.Helper()
	for _, variant := range []struct {
		tag, attribute, prefix string
	}{
		{"tbody", "id", "variable-row-"},
		{"li", "data-mobile-variable", ""},
	} {
		var groups int
		for group := range document.Descendants() {
			if group.Data != variant.tag || !slices.Contains(
				group.Attr,
				html.Attribute{Key: "data-variable-group", Val: name},
			) {
				continue
			}
			groups++
			var labels int
			var rows []string
			for child := range group.Descendants() {
				if child.Type == html.TextNode && child.Data == name {
					labels++
				}
				for _, attr := range child.Attr {
					if attr.Key == variant.attribute &&
						strings.HasPrefix(attr.Val, variant.prefix) {
						rows = append(rows, attr.Val)
					}
				}
			}
			var want []string
			for _, id := range ids {
				want = append(want, fmt.Sprintf("%s%d", variant.prefix, id))
			}
			if labels != 1 || !slices.Equal(rows, want) {
				t.Errorf("%s group %s: labels=%d, rows=%v, want %v",
					variant.tag, name, labels, rows, want)
			}
		}
		if groups != 1 {
			t.Errorf("%s: expected one group for %s, got %d",
				variant.tag, name, groups)
		}
	}
}
