package events

import (
	"encoding/json"
	"testing"
)

func TestNotificationHistoryHidesLegacyCredentialErrors(t *testing.T) {
	// Given: an older retained delivery error includes a credential-bearing URL.
	raw := `{"slack":"failed: https://hooks.example/legacy-secret timeout","email":"ok","gotify":"skipped"}`
	// When: the public read projection prepares delivery results.
	value := PublicNotificationResults(raw)
	// Then: failure/success classifications survive without stored error text.
	var results map[string]string
	if err := json.Unmarshal([]byte(value), &results); err != nil {
		t.Fatal(err)
	}
	if results["slack"] != "failed: delivery failed" ||
		results["email"] != "ok" ||
		results["gotify"] != "skipped" {
		t.Fatal(results)
	}
}

func TestNotificationHistoryOnlyAllowsExactEmailWarning(t *testing.T) {
	raw := `{"email":"ok: sent without TLS","slack":"ok: sent without TLS","gotify":"ok: sent without TLS provider-secret"}`
	var results map[string]string
	if err := json.Unmarshal([]byte(PublicNotificationResults(raw)),
		&results); err != nil {
		t.Fatal(err)
	}
	if results["email"] != SMTPPlaintextStatus ||
		results["slack"] != "failed: delivery failed" ||
		results["gotify"] != "failed: delivery failed" {
		t.Fatal(results)
	}
}
