package artifact

import (
	"bytes"
	"encoding/json"
	"strings"

	"durpdeploy/internal/logscrub"
)

type TerraformResourceChange struct {
	Address string   `json:"address"`
	Actions []string `json:"actions"`
	Before  string   `json:"before"`
	After   string   `json:"after"`
}

// TerraformReview exposes only resource changes, never configuration or outputs.
func TerraformReview(
	raw []byte, secrets []string,
) ([]TerraformResourceChange, error) {
	var plan struct {
		FormatVersion string `json:"format_version"`
		Resources     []struct {
			Address string `json:"address"`
			Change  struct {
				Actions         []string `json:"actions"`
				Before          any      `json:"before"`
				After           any      `json:"after"`
				BeforeSensitive any      `json:"before_sensitive"`
				AfterSensitive  any      `json:"after_sensitive"`
				AfterUnknown    any      `json:"after_unknown"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&plan); err != nil ||
		!strings.HasPrefix(plan.FormatVersion, "1.") {
		return nil, ErrGateConfig
	}
	if _, err := ParseGateReview(raw, "terraform"); err != nil {
		return nil, err
	}
	scrubber := logscrub.New(secrets)
	changes := make([]TerraformResourceChange, 0, len(plan.Resources))
	for _, resource := range plan.Resources {
		change := resource.Change
		before := terraformValue(change.Before, change.BeforeSensitive, nil)
		after := terraformValue(change.After, change.AfterSensitive,
			change.AfterUnknown)
		beforeJSON, err := json.MarshalIndent(
			scrubTerraformValue(before, scrubber),
			"",
			"  ",
		)
		if err != nil {
			return nil, ErrGateConfig
		}
		afterJSON, err := json.MarshalIndent(
			scrubTerraformValue(after, scrubber),
			"",
			"  ",
		)
		if err != nil {
			return nil, ErrGateConfig
		}
		changes = append(changes, TerraformResourceChange{
			Address: scrubber.Scrub(resource.Address), Actions: change.Actions,
			Before: string(beforeJSON), After: string(afterJSON),
		})
	}
	return changes, nil
}

// Missing or malformed sensitivity metadata fails closed for that subtree.
func terraformValue(value, sensitive, unknown any) any {
	if sensitive == nil || sensitive == true {
		if value == nil && unknown == nil {
			return nil
		}
		return "[REDACTED]"
	}
	if unknown == true {
		return "(known after apply)"
	}
	switch data := value.(type) {
	case map[string]any:
		masks, ok := sensitive.(map[string]any)
		if !ok && sensitive != false {
			return "[REDACTED]"
		}
		unknowns, ok := unknown.(map[string]any)
		if unknown != nil && unknown != false && !ok {
			return "[REDACTED]"
		}
		result := make(map[string]any, len(data)+len(unknowns))
		for key, item := range data {
			mask, exists := masks[key]
			if exists && mask == nil {
				return "[REDACTED]"
			}
			if !exists {
				mask = false
			}
			result[key] = terraformValue(item, mask, unknowns[key])
		}
		for key, mask := range unknowns {
			if _, exists := data[key]; !exists {
				sensitiveMask, exists := masks[key]
				if exists && sensitiveMask == nil {
					return "[REDACTED]"
				}
				if !exists {
					sensitiveMask = false
				}
				result[key] = terraformValue(nil, sensitiveMask, mask)
			}
		}
		return result
	case []any:
		masks, ok := sensitive.([]any)
		if !ok && sensitive != false {
			return "[REDACTED]"
		}
		if ok && len(masks) != len(data) {
			return "[REDACTED]"
		}
		unknowns, ok := unknown.([]any)
		if unknown != nil && unknown != false && !ok {
			return "[REDACTED]"
		}
		if ok && len(unknowns) != len(data) {
			return "[REDACTED]"
		}
		result := make([]any, len(data))
		for index, item := range data {
			var mask, pending any = false, nil
			if index < len(masks) {
				mask = masks[index]
			}
			if index < len(unknowns) {
				pending = unknowns[index]
			}
			result[index] = terraformValue(item, mask, pending)
		}
		return result
	default:
		if sensitive != false || (unknown != nil && unknown != false) {
			return "[REDACTED]"
		}
		return value
	}
}

func scrubTerraformValue(value any, scrubber *logscrub.Scrubber) any {
	switch data := value.(type) {
	case string:
		return scrubber.Scrub(data)
	case json.Number:
		if scrubber.Scrub(string(data)) != string(data) {
			return "[REDACTED]"
		}
	case map[string]any:
		result := make(map[string]any, len(data))
		for key, item := range data {
			result[scrubber.Scrub(key)] = scrubTerraformValue(item, scrubber)
		}
		return result
	case []any:
		for index, item := range data {
			data[index] = scrubTerraformValue(item, scrubber)
		}
	}
	return value
}
