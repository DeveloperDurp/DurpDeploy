package httpstream

import (
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestStepLogFromRowUsesPersistedDisplayTime(t *testing.T) {
	for _, test := range []struct {
		name  string
		month time.Month
		want  string
	}{
		{"winter", time.January, "2000-01-02 03:04:05"},
		{"summer", time.July, "2000-07-02 03:04:05"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given: a persisted time in the server's local timezone.
			created := time.Date(2000, test.month, 2, 3, 4, 5, 0, time.Local)
			// When: preparing a structured stream event.
			log := StepLogFromRow(db.DeploymentLog{CreatedAt: created.Unix()})
			// Then: display time comes from that log, in the page's format.
			if log.CreatedAt != created.Unix() ||
				log.DisplayTimestamp != test.want {
				t.Fatalf("persisted display time changed: %+v", log)
			}
		})
	}
}
