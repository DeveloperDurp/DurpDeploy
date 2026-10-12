package artifact

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateZIPBoundsImplicitDirectories(t *testing.T) {
	// Given: few archive entries, but enough distinct parents to exceed inodes.
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for index := 0; index < 21; index++ {
		name := fmt.Sprintf("%d/", index) + strings.Repeat("a/", 999) + "file"
		if _, err := writer.Create(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "implicit-parents.zip")
	if err := os.WriteFile(filename, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	// When
	err := ValidateZIP(t.Context(), filename)
	// Then
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded implicit directories accepted: %v", err)
	}
}
