//go:build e2e && agentstaging

package agentserver_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/events"
)

type stagingEventRecorder struct {
	notifications chan<- events.Event
}

func (stagingEventRecorder) Name() string { return "staging-test" }

func (r stagingEventRecorder) Notify(
	ctx context.Context, event events.Event,
) (bool, error) {
	select {
	case r.notifications <- event:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (s *stagingE2E) assertConflictNotificationsRedacted(
	t *testing.T, deploymentID int64, logs string,
) {
	t.Helper()
	if strings.Contains(logs, "selected") ||
		!strings.Contains(logs, "[REDACTED]") {
		t.Fatalf("conflict log did not redact its secret path: %s", logs)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-s.notifications:
			if event.DeploymentID != deploymentID ||
				event.Type != events.DeploymentFailed {
				continue
			}
			if strings.Contains(event.Message, "selected") ||
				!strings.Contains(event.Message, "[REDACTED]") {
				t.Fatalf(
					"notifier received an unredacted conflict: %q",
					event.Message,
				)
			}
		case <-deadline.C:
			t.Fatal("conflict failure notification did not arrive")
		}
		break
	}
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		data := string(s.api(t, "GET", "/admin/notifications", nil, 200))
		if !strings.Contains(data, "conflicting staging changes") {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if strings.Contains(data, "selected") ||
			!strings.Contains(data, "[REDACTED]") {
			t.Fatalf(
				"notification API exposed an unredacted conflict: %s",
				data,
			)
		}
		return
	}
	t.Fatal("persisted conflict notification did not arrive")
}
