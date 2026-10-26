package artifact

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZIPAcceptsSharedParentsAndExplicitDirectory(t *testing.T) {
	// Given: an implicit parent can be declared once without losing files.
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range []string{"app/file.txt", "app/", "app/other.txt"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if name != "app/" {
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
	for _, name := range []string{"app/file.txt", "app/other.txt"} {
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(contents) != "contents" {
			t.Fatalf("%s contents=%q error=%v", name, contents, err)
		}
	}
}
