package artifact

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzValidateZIPPaths(f *testing.F) {
	for _, pair := range [][2]string{
		{"app.txt", "nested/config.json"}, {"dir/", "dir/file"},
		{"dir/", "dir/"}, {"a", "a/b"}, {"a/b", "a"}, {"a", "a"},
		{"../escape", ""}, {"/absolute", ""}, {`a\escape`, ""},
		{"C:escape", ""}, {"a/../escape", ""}, {"a//b", ""},
		{".", ""}, {"", ""}, {"a\x00b", ""}, {"é/file", ""},
		{strings.Repeat("a", 256), ""}, {strings.Repeat("a/", 2001), ""},
	} {
		for kind := uint8(0); kind < 4; kind++ {
			f.Add(pair[0], pair[1], kind)
		}
	}
	f.Fuzz(func(t *testing.T, first, second string, kind uint8) {
		if len(first) > 4096 || len(second) > 4096 {
			t.Skip()
		}
		modes := []os.FileMode{
			0o644, os.ModeDir | 0o755,
			os.ModeSymlink | 0o777, os.ModeNamedPipe | 0o600,
		}
		mode := modes[int(kind)%len(modes)]
		// ZIP names ending in a slash represent directories on readback.
		if strings.HasSuffix(first, "/") {
			mode |= os.ModeDir
		}
		names := []string{first}
		if second != "" {
			names = append(names, second)
		}
		filename := writeFuzzZIP(t, names, mode)
		// Validation reads fixed stored payloads. Never extract fuzzed paths.
		if ValidateZIP(t.Context(), filename) != nil {
			return
		}
		assertFuzzZIPPaths(t, names, mode)
	})
}

func writeFuzzZIP(t *testing.T, names []string, mode os.FileMode) string {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for index, name := range names {
		entryMode := os.FileMode(0o644)
		if index == 0 {
			entryMode = mode
		}
		if entryMode.IsDir() && !strings.HasSuffix(name, "/") {
			name += "/"
			names[index] = name
		}
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetMode(entryMode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal("could not construct bounded ZIP fixture")
		}
		if !strings.HasSuffix(name, "/") {
			if _, err := entry.Write([]byte("synthetic")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "package.zip")
	if err := os.WriteFile(filename, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func assertFuzzZIPPaths(t *testing.T, names []string, mode os.FileMode) {
	t.Helper()
	if !mode.IsRegular() && !mode.IsDir() {
		t.Fatal("accepted a special filesystem entry")
	}
	for _, name := range names {
		name = strings.TrimSuffix(name, "/")
		root := filepath.Join(t.TempDir(), "staging")
		rel, err := filepath.Rel(root, filepath.Join(root, name))
		if err != nil || !filepath.IsLocal(rel) || !fs.ValidPath(name) ||
			name == "." || strings.ContainsAny(name, "\\:\x00") {
			t.Fatal("accepted an escaping or noncanonical entry path")
		}
	}
	if len(names) == 2 {
		firstPath := strings.TrimSuffix(names[0], "/")
		secondPath := strings.TrimSuffix(names[1], "/")
		firstDir := strings.HasSuffix(names[0], "/")
		secondDir := strings.HasSuffix(names[1], "/")
		if (firstPath == secondPath && (!firstDir || !secondDir)) ||
			(!firstDir && strings.HasPrefix(secondPath, firstPath+"/")) ||
			(!secondDir && strings.HasPrefix(firstPath, secondPath+"/")) {
			t.Fatal("accepted duplicate files or a file as a parent")
		}
	}
}
