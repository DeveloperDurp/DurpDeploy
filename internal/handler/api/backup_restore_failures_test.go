//go:build e2e && backuprestore && linux

package api_test

import (
	"crypto/rand"
	"encoding/base64"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func restoreTruncatedArchive(t *testing.T, path string) string {
	t.Helper()
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	}()
	target := path + ".truncated"
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := io.CopyN(file, source, 32); err != nil {
		t.Fatal(err)
	}
	return target
}

func (f *restoreFixture) assertSQLiteCorrupt(t *testing.T) {
	t.Helper()
	broken := filepath.Join(f.root, "backup", "broken")
	if err := os.Mkdir(broken, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(
		broken,
		os.DirFS(filepath.Join(f.root, "backup", "replica")),
	); err != nil {
		t.Fatal(err)
	}
	count := 0
	err := filepath.WalkDir(
		broken,
		func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".lz4") {
				count++
				return os.Truncate(path, 1)
			}
			return nil
		},
	)
	if err != nil || count == 0 {
		t.Fatalf("truncate replica: %v files=%d", err, count)
	}
	c := f.litestreamContainer(t, "restore", "-o", "/recovery/backup/broken.db",
		"file:///recovery/backup/broken")
	if restoreContainerExit(t, c) == 0 {
		t.Fatal("restore accepted a truncated replica")
	}
	restoreTerminate(t, c)
	if err := os.Remove(
		filepath.Join(f.root, "backup", "broken.db"),
	); err != nil &&
		!os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func (f *restoreFixture) assertWrongKey(t *testing.T) {
	t.Helper()
	matching, err := os.ReadFile(f.key)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		f.key,
		[]byte(base64.StdEncoding.EncodeToString(key)),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	p := f.start(t)
	// The public variable is encrypted at rest and this read must decrypt it.
	response := f.api.api(t, "GET", f.api.base()+"/variables", nil, 500)
	if strings.Contains(string(response), f.secret) ||
		strings.Contains(string(response), "retained-public-value") {
		t.Fatal("mismatched key disclosed encrypted fixture data")
	}
	p.stop(t)
	if err := os.WriteFile(f.key, matching, 0600); err != nil {
		t.Fatal(err)
	}
}
