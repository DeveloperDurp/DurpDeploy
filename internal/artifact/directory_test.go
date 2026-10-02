package artifact

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZIPAcceptsRepeatedDirectoryEntries(t *testing.T) {
	// Given: directories may be repeated, but files cannot be overwritten.
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range []string{"app/", "app/", "app/file.txt"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if name == "app/file.txt" {
			if _, err := entry.Write([]byte("contents")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "package.zip")
	if err := os.WriteFile(filename, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	// When
	directory, err := ExtractZIP(t.Context(), filename)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	contents, err := os.ReadFile(filepath.Join(directory, "app/file.txt"))
	if err != nil || string(contents) != "contents" {
		t.Fatalf("contents=%q error=%v", contents, err)
	}
}
