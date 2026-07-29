package swagger

import (
	"embed"
)

// SpecJSON is the generated OpenAPI 2.0 spec.
//
//go:embed spec.json
var SpecJSON embed.FS

// ReadSpec returns the generated spec bytes.
func ReadSpec() ([]byte, error) {
	return SpecJSON.ReadFile("spec.json")
}
