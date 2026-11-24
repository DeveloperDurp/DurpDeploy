package artifact

import (
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
	if !validGatePath(artifactPath) || !validGatePath(reviewPath) {
		return ErrGateConfig
	}
	if artifactPath == reviewPath ||
		(format != "summary" && format != "terraform") {
		return ErrGateConfig
	}
	return nil
}

func validGatePath(path string) bool {
	return len(path) <= 128 && fs.ValidPath(path) && path != "." &&
		!strings.ContainsAny(path, "\\\x00")
}

// GateReview deliberately exposes counts only, never names, values or outputs.
type GateReview struct {
	Create int64 `json:"create"`
	Update int64 `json:"update"`
	Delete int64 `json:"delete"`
	Read   int64 `json:"read"`
}
