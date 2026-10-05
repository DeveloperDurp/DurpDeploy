package artifact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTerraformReviewMasksNestedSecretsAndUnknowns(t *testing.T) {
	// Given: public attributes, nested marked secrets, and unknown values.
	raw := []byte(
		`{"format_version":"1.2","resource_changes":[{"address":"example.test","change":{"actions":["delete","create"],"before":{"name":"old","nested":[{"secret":"old-secret"}]},"after":{"name":"new","nested":[{"secret":"new-secret"}]},"before_sensitive":{"nested":[{"secret":true}]},"after_sensitive":{"nested":[{"secret":true}]},"after_unknown":{"id":true}}}],"output_changes":{"password":"output-secret"}}`,
	)
	// When: the review is rendered for inspection.
	changes, err := TerraformReview(raw, nil)
	// Then: public values and action order remain; secret and output data do not.
	if err != nil || len(changes) != 1 {
		t.Fatalf("changes=%v err=%v", changes, err)
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"old-secret", "new-secret", "output-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("secret exposed")
		}
	}
	if !strings.Contains(changes[0].After, `"name": "new"`) ||
		!strings.Contains(changes[0].After, "known after apply") ||
		!strings.Contains(changes[0].Before, "[REDACTED]") ||
		strings.Join(changes[0].Actions, ",") != "delete,create" {
		t.Fatalf("masked change=%v", changes[0])
	}
}

func TestTerraformReviewMissingOrMalformedMasksHideValues(t *testing.T) {
	for _, mask := range []string{"null", `"invalid"`, "true", `{"value":null}`} {
		// Given: untrustworthy sensitivity metadata.
		raw := []byte(
			`{"format_version":"1.2","resource_changes":[{"change":{"actions":["create"],"after":{"value":"secret"},"after_sensitive":` + mask + `}}]}`,
		)
		// When: the review is parsed.
		changes, err := TerraformReview(raw, nil)
		// Then: the complete subtree is hidden.
		if err != nil || len(changes) != 1 ||
			changes[0].After != `"[REDACTED]"` {
			t.Fatalf("mask=%s changes=%v err=%v", mask, changes, err)
		}
	}
}

func TestTerraformReviewScrubsDecodedReleaseSecrets(t *testing.T) {
	// Given: a secret requiring JSON escaping, in an address, key and value.
	secret := "private\n\"value"
	change := map[string]any{
		"address": "resource." + secret,
		"change": map[string]any{"actions": []string{"create"},
			"after":           map[string]any{secret: "prefix:" + secret},
			"after_sensitive": map[string]any{}},
	}
	raw, err := json.Marshal(
		map[string]any{
			"format_version":   "1.2",
			"resource_changes": []any{change},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	// When: decoded values are scrubbed before display encoding.
	changes, err := TerraformReview(raw, []string{secret})
	// Then: no escaped or unescaped secret survives.
	if err != nil || len(changes) != 1 {
		t.Fatalf("err=%v", err)
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") ||
		!strings.Contains(changes[0].Address, "[REDACTED]") {
		t.Fatal("release secret exposed")
	}
}

func TestTerraformReviewShortArrayMaskHidesEntireArray(t *testing.T) {
	// Given: sensitivity metadata does not cover the second array item.
	raw := []byte(
		`{"format_version":"1.2","resource_changes":[{"change":{"actions":["create"],"after":["public","secret"],"after_sensitive":[false]}}]}`,
	)
	// When: the review is parsed.
	changes, err := TerraformReview(raw, nil)
	// Then: the malformed array metadata cannot expose any element.
	if err != nil || len(changes) != 1 || changes[0].After != `"[REDACTED]"` {
		t.Fatalf("changes=%v err=%v", changes, err)
	}
}
