//go:build e2e

package api_test

import (
	"archive/zip"
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (f *artifactE2E) verifyPackageDirectoryEntries(t *testing.T) {
	t.Helper()
	defer f.changePackage("package")
	for _, scenario := range []struct {
		name    string
		entries []string
		status  int
	}{
		{"shared-parent", []string{"app/file", "app/", "app/other"}, 200},
		{"duplicate-directory", []string{"app/", "app/", "app/file"}, 422},
		{"duplicate-after-child", []string{"app/file", "app/", "app/"}, 422},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			filename := packageDirectoryArchive(t, scenario.entries)
			f.changePackage("file:" + filename)
			path := f.base() + "/package-repository/test"
			body := f.api(
				t,
				"POST",
				path,
				map[string]string{
					"version": "directory-fixture",
				},
				scenario.status,
			)
			page := f.web(t, "POST", strings.TrimPrefix(path, "/api/v1"),
				url.Values{"version": {"directory-fixture"}}, 200)
			if bytes.Contains(body, []byte("artifact-secret")) ||
				strings.Contains(page, "artifact-secret") {
				t.Fatal("package check exposed repository credentials")
			}
			if strings.Contains(page, `data-package-test="success"`) !=
				(scenario.status == 200) {
				t.Fatal("web package result does not match validation")
			}
			if scenario.status == 422 && !strings.Contains(page,
				"Could not retrieve and validate this ZIP.") {
				t.Fatal("web package rejection has no validation error")
			}
		})
	}
}

func packageDirectoryArchive(t *testing.T, names []string) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(name, "/") {
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
	return filename
}
