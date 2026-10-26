package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"

	"durpdeploy/internal/logscrub"
)

const terraformRedacted = "[REDACTED]"

type TerraformReviewResponse struct {
	Resources        []TerraformResourceChange `json:"resources"`
	ReviewSource     string                    `json:"review_source"`
	ReviewVerified   bool                      `json:"review_verified"`
	PreviewTruncated bool                      `json:"preview_truncated"`
	ResourcesOmitted int64                     `json:"resources_omitted"`
}

type TerraformResourceChange struct {
	Address       string   `json:"address"`
	Actions       []string `json:"actions"`
	Before        string   `json:"before"`
	After         string   `json:"after"`
	OmittedFields []string `json:"omitted_fields,omitempty"`
}

// TerraformReview exposes only resource changes, never configuration or outputs.
func TerraformReview(
	raw []byte, secrets []string,
) ([]TerraformResourceChange, error) {
	return TerraformReviewReader(bytes.NewReader(raw), secrets)
}

// TerraformReviewReader discards state and configuration before rendering each resource.
func TerraformReviewReader(
	reader io.Reader, secrets []string,
) ([]TerraformResourceChange, error) {
	preview, err := TerraformPreviewReader(reader, secrets)
	return preview.Resources, err
}

// TerraformPreviewReader bounds display details without limiting retained artifacts.
func TerraformPreviewReader(
	reader io.Reader,
	secrets []string,
) (TerraformReviewResponse, error) {
	return TerraformPreviewReaderWithScrubber(reader, logscrub.New(secrets))
}

func TerraformPreviewReaderWithScrubber(
	reader io.Reader, scrubber *logscrub.Scrubber,
) (TerraformReviewResponse, error) {
	s := newReviewJSON(reader)
	s.preview = &terraformPreview{}
	_, changes, err := s.terraform(
		func(change terraformResource) (TerraformResourceChange, error) {
			return renderTerraformResource(change, scrubber)
		},
	)
	if err == nil {
		err = s.finish()
	}
	if err != nil {
		return TerraformReviewResponse{}, errors.Join(ErrGateConfig, err)
	}
	return TerraformReviewResponse{
		Resources:        changes,
		ReviewSource:     "step_output",
		PreviewTruncated: s.preview.truncated,
		ResourcesOmitted: s.preview.omitted,
	}, nil
}

// Missing or malformed sensitivity metadata fails closed for that subtree.
func terraformValue(value, sensitive, unknown any) any {
	if sensitive == nil || sensitive == true {
		if value == nil && unknown == nil {
			return nil
		}
		return terraformRedacted
	}
	if unknown == true {
		return "(known after apply)"
	}
	switch data := value.(type) {
	case map[string]any:
		return terraformObject(data, sensitive, unknown)
	case []any:
		return terraformArray(data, sensitive, unknown)
	default:
		if sensitive != false || (unknown != nil && unknown != false) {
			return terraformRedacted
		}
		return value
	}
}

func terraformObject(data map[string]any, sensitive, unknown any) any {
	masks, ok := sensitive.(map[string]any)
	if !ok && sensitive != false {
		return terraformRedacted
	}
	unknowns, ok := unknown.(map[string]any)
	if unknown != nil && unknown != false && !ok {
		return terraformRedacted
	}
	result := make(map[string]any, len(data)+len(unknowns))
	for key := range unknowns {
		result[key] = nil
	}
	maps.Copy(result, data)
	for key, item := range result {
		mask, exists := masks[key]
		if exists && mask == nil {
			return terraformRedacted
		}
		if !exists {
			mask = false
		}
		result[key] = terraformValue(item, mask, unknowns[key])
	}
	return result
}

func terraformArray(data []any, sensitive, unknown any) any {
	masks, ok := sensitive.([]any)
	if !ok && sensitive != false {
		return terraformRedacted
	}
	if ok && len(masks) != len(data) {
		return terraformRedacted
	}
	unknowns, ok := unknown.([]any)
	if unknown != nil && unknown != false && !ok {
		return terraformRedacted
	}
	if ok && len(unknowns) != len(data) {
		return terraformRedacted
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
}

func scrubTerraformValue(value any, scrubber *logscrub.Scrubber) any {
	switch data := value.(type) {
	case string:
		return scrubber.Scrub(data)
	case json.Number:
		if scrubber.Scrub(string(data)) != string(data) {
			return terraformRedacted
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
