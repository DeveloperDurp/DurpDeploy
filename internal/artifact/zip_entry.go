package artifact

import (
	"archive/zip"
	"io/fs"
	"path"
	"strings"
)

// Staging uses Linux filesystems: 255-byte components and room for mount prefixes.
const maxZIPPathBytes = 4000
const maxZIPComponentBytes = 255

func validateZIPEntry(entry *zip.File, kinds map[string]bool) error {
	name := strings.TrimSuffix(entry.Name, "/")
	if len(name) > maxZIPPathBytes {
		return ErrInvalid
	}
	for _, component := range strings.Split(name, "/") {
		if len(component) > maxZIPComponentBytes {
			return ErrInvalid
		}
	}
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
