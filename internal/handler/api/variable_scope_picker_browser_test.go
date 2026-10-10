//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

// selectVariableScope clicks the rendered base-select picker; it does not set
// the scope through JavaScript. The fixture Chromium supports base-select.
func (b *packageBrowser) selectVariableScope(
	t *testing.T, selector string, eligible int64,
) {
	t.Helper()
	var option int
	decodeStepLogTest(t, b.evaluate(t, fmt.Sprintf(`(() => {
	 const form = document.querySelector(%q);
	 const select = form?.elements.namedItem('environment_id');
	 if (!select || select.disabled || !select.getClientRects().length) return -1;
	 select.focus();
	 return [...select.options].findIndex(o => o.value === %q && !o.disabled);
	})()`, selector, fmt.Sprint(eligible))), &option)
	if option < 1 {
		t.Fatal(
			"eligible scope unavailable before deployment",
		)
	}
	click := func(expression string) {
		var point struct{ X, Y float64 }
		decodeStepLogTest(
			t,
			b.evaluate(t, fmt.Sprintf(`(() => {
	 const element = %s;
	 element.scrollIntoView({block:'center'});
	 const box = element.getBoundingClientRect();
	 return {X: box.x + box.width / 2, Y: box.y + box.height / 2};
	})()`, expression)),
			&point,
		)
		for _, kind := range []string{"mousePressed", "mouseReleased"} {
			b.call(
				t,
				"Input.dispatchMouseEvent",
				map[string]any{
					"type": kind, "x": point.X, "y": point.Y,
					"button": "left", "clickCount": 1,
				},
				&struct{}{},
			)
		}
	}
	selectExpression := fmt.Sprintf(
		`document.querySelector(%q).elements.namedItem('environment_id')`,
		selector,
	)
	click(selectExpression)
	b.wait(t, selectExpression+`.matches(':open')`)
	click(
		fmt.Sprintf(
			"%s.options[%d]",
			selectExpression,
			option,
		),
	)
	b.wait(t, fmt.Sprintf(
		`document.querySelector(%q).elements.namedItem('environment_id').value === %q`,
		selector,
		fmt.Sprint(eligible),
	))
}
