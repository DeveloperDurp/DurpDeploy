package handler

import (
	"encoding/json"
	"errors"
	"strconv"
)

// runbookEditorSteps keeps int64 retry values exact through JSON.parse and
// Alpine's hidden form inputs. Public API snapshots retain numeric values.
func runbookEditorSteps(raw string) (string, error) {
	var steps []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &steps); err != nil {
		return "", err
	}
	for _, step := range steps {
		if step == nil {
			return "", errors.New("invalid runbook step")
		}
		var retries int64
		if value, ok := step["max_retries"]; ok {
			if err := json.Unmarshal(value, &retries); err != nil {
				return "", err
			}
		}
		step["max_retries"] = json.RawMessage(
			strconv.Quote(strconv.FormatInt(retries, 10)),
		)
	}
	encoded, err := json.Marshal(steps)
	return string(encoded), err
}
