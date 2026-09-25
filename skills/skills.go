// Package skills embeds the bundled agent skill files and exposes their
// frontmatter so the discovery endpoints stay in sync with the files.
package skills

import (
	_ "embed"
	"strings"
)

//go:embed durpdeploy/SKILL.md
var SkillMD string

// SkillName is the directory name of the bundled skill.
const SkillName = "durpdeploy"

// SkillDescription is parsed from the YAML frontmatter at init so
// SKILL.md stays the single source of truth.
var SkillDescription = parseDescription(SkillMD)

// parseDescription extracts the `description:` value from the YAML
// frontmatter. The value is a single plain line, so a line scan is
// enough; no YAML dependency needed.
func parseDescription(md string) string {
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return ""
}
