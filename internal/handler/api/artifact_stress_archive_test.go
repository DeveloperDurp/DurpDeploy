//go:build e2e && artifactstress

package api_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/artifact"
)

func nearLimitArtifact(t *testing.T) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "near-limit.zip")
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	writer := zip.NewWriter(file)
	base := artifact.MaxExtracted / int64(artifact.MaxFiles)
	remainder := artifact.MaxExtracted % int64(artifact.MaxFiles)
	payload := bytes.Repeat([]byte("x"), int(base+1))
	for index := 0; index < artifact.MaxFiles; index++ {
		name := fmt.Sprintf("d%04d/package.txt", index)
		if index == artifact.MaxFiles-1 {
			name = "package.txt"
		}
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		size := base
		if int64(index) < remainder {
			size++
		}
		if _, err := entry.Write(payload[:size]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > artifact.MaxDownload {
		t.Fatal("stress archive exceeds compressed download limit")
	}
	return filename
}
