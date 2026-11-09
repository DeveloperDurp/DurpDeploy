package handler_test

import (
	"context"
	"encoding/json"
	"testing"
)

func assertPasskeyWarningAudit(
	t *testing.T, h *authHarness, userID int64, purpose string, count int,
) {
	t.Helper()
	rows, err := h.repo.Queries.ListAuditLogs(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, row := range rows {
		if row.Action != "mfa_passkey_clone_warning" {
			continue
		}
		found++
		if !row.UserID.Valid || row.UserID.Int64 != userID ||
			row.EntityType != "user" || row.EntityID.Int64 != userID {
			t.Fatal("warning event lost verified user attribution")
		}
		var details map[string]any
		if err := json.Unmarshal([]byte(row.Details.String), &details); err != nil {
			t.Fatal(err)
		}
		if details["purpose"] != purpose || details["credential_id"] == "" ||
			len(details) != 6 {
			t.Fatalf("unexpected warning event details: %v", details)
		}
	}
	if found != count {
		t.Fatalf("warning events = %d, want %d", found, count)
	}
}
