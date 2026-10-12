package events

import "encoding/json"

// PublicNotificationResults also hides credential-bearing errors retained by
// older versions. Only status classifications cross the HTTP read boundary.
func PublicNotificationResults(raw string) string {
	var results map[string]string
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		return "{}"
	}
	for channel, status := range results {
		switch status {
		case "ok", "skipped":
		default:
			results[channel] = "failed: delivery failed"
		}
	}
	value, err := json.Marshal(results)
	if err != nil {
		return "{}"
	}
	return string(value)
}
