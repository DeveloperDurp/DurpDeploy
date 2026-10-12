package events_test

import (
	"encoding/json"
	"errors"
	"testing"

	"durpdeploy/internal/events"
)

func TestNotificationDeliveryWarnings(t *testing.T) {
	for _, tc := range []struct {
		name, channel, want string
		err                 error
		skipped             bool
	}{
		{"plaintext", "email", events.SMTPPlaintextStatus,
			events.SMTPPlaintextWarning{}, false},
		{"encrypted", "email", "ok", nil, false},
		{"skipped", "email", "skipped", events.SMTPPlaintextWarning{}, true},
		{"failure", "email", "failed: delivery failed",
			errors.New("provider-secret"), false},
		{"joined failure", "email", "failed: delivery failed",
			errors.Join(events.SMTPPlaintextWarning{},
				errors.New("provider-secret")), false},
		{"other channel", "slack", "failed: delivery failed",
			events.SMTPPlaintextWarning{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepo(t)
			bus := events.NewBus(repo)
			bus.Register(&fakeNotifier{
				name: tc.channel, failErr: tc.err, skip: tc.skipped,
			})
			bus.Publish(t.Context(), events.Event{Type: events.BackupHealthy})
			rows, err := repo.Queries.ListNotificationEvents(t.Context(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("history rows = %d, want 1", len(rows))
			}
			var results map[string]string
			if err := json.Unmarshal([]byte(events.PublicNotificationResults(
				rows[0].Results)), &results); err != nil {
				t.Fatal(err)
			}
			if results[tc.channel] != tc.want {
				t.Fatalf("result = %q, want %q", results[tc.channel], tc.want)
			}
		})
	}
}
