package artifact

import (
	"archive/zip"
	"io/fs"
	"path"
	"strings"
)

func validateZIPEntry(entry *zip.File, kinds map[string]bool) error {
	name := strings.TrimSuffix(entry.Name, "/")
	if name == "." || !fs.ValidPath(name) || path.Clean(name) != name ||
		strings.ContainsAny(name, "\\:\x00") {
		return ErrInvalid
	}
	mode := entry.Mode()
	if !mode.IsRegular() && !mode.IsDir() {
		return ErrInvalid
	}
	if directoryEntry, exists := kinds[name]; exists &&
		(!directoryEntry || !mode.IsDir()) {
		return ErrInvalid
	}
	kinds[name] = mode.IsDir()
	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		if directoryEntry, exists := kinds[parent]; exists && !directoryEntry {
			return ErrInvalid
		}
		kinds[parent] = true
	}
	return nil
}
