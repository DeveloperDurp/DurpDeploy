package artifact

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
)

var ErrGateConfig = errors.New("invalid artifact approval configuration")

// ValidateGateConfig parses the paths relative to DURPDEPLOY_STAGE_DIR.
func ValidateGateConfig(
	target, network, artifactPath, reviewPath, format string,
) error {
	if network != "" && network != "none" && network != "bridge" {
		return ErrGateConfig
	}
	if target == "agent" && (network == "bridge" || artifactPath != "") {
		return ErrGateConfig
	}
	if artifactPath == "" {
		if reviewPath != "" || format != "" {
			return ErrGateConfig
		}
		return nil
	}
	for _, path := range []string{artifactPath, reviewPath} {
		if len(path) > 128 || !fs.ValidPath(path) || path == "." ||
			strings.ContainsAny(path, "\\\x00") {
			return ErrGateConfig
		}
	}
	if artifactPath == reviewPath ||
		(format != "summary" && format != "terraform") {
		return ErrGateConfig
	}
	return nil
}

// GateReview deliberately exposes counts only, never names, values or outputs.
type GateReview struct {
	Create int64 `json:"create"`
	Update int64 `json:"update"`
	Delete int64 `json:"delete"`
	Read   int64 `json:"read"`
}

func ParseGateReview(raw []byte, format string) (GateReview, error) {
	var result GateReview
	if format == "terraform" {
		var plan struct {
			ResourceChanges []struct {
				Change struct {
					Actions []string `json:"actions"`
				} `json:"change"`
			} `json:"resource_changes"`
			FormatVersion string `json:"format_version"`
		}
		if err := json.Unmarshal(
			raw,
			&plan,
		); err != nil ||
			plan.FormatVersion == "" {
			return result, ErrGateConfig
		}
		for _, resource := range plan.ResourceChanges {
			for _, action := range resource.Change.Actions {
				switch action {
				case "create":
					result.Create++
				case "update":
					result.Update++
				case "delete":
					result.Delete++
				case "read":
					result.Read++
				case "no-op":
				default:
					return GateReview{}, ErrGateConfig
				}
			}
		}
	} else if err := json.Unmarshal(raw, &result); err != nil {
		return result, ErrGateConfig
	}
	for _, count := range []int64{result.Create, result.Update, result.Delete, result.Read} {
		if count < 0 {
			return GateReview{}, ErrGateConfig
		}
	}
	return result, nil
}
