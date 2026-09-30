package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func zipFixture(t *testing.T, name string, mode os.FileMode) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("package contents")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestExtractZIPRejectsUnsafeEntries(t *testing.T) {
	for _, fixture := range []struct {
		name string
		mode os.FileMode
	}{
		{"../escape", 0644}, {"/absolute", 0644}, {"a\\escape", 0644},
		{"C:escape", 0644}, {"a/../escape", 0644}, {"link", os.ModeSymlink | 0644},
		{"pipe", os.ModeNamedPipe | 0644},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// Given
			filename := filepath.Join(t.TempDir(), "package.zip")
			if err := os.WriteFile(
				filename,
				zipFixture(t, fixture.name, fixture.mode),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			// When
			_, err := ExtractZIP(t.Context(), filename)
			// Then
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid ZIP, got %v", err)
			}
		})
	}
}

func TestFetchPinsAndExtractsZIP(t *testing.T) {
	// Given
	data := zipFixture(t, "nested/app.txt", 0755)
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer secret" {
				w.WriteHeader(401)
				return
			}
			if _, err := w.Write(data); err != nil {
				t.Error(err)
			}
		}),
	)
	defer server.Close()
	client := &Client{HTTP: server.Client()}
	source := Repository{
		URLTemplate: server.URL + "/{version}.zip",
		AuthType:    "bearer",
		Credential:  "secret",
	}
	resolved, err := source.URL("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	// When
	download, err := client.Fetch(t.Context(), source, Pin{URL: resolved})
	// Then
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(download.Path); err != nil {
			t.Error(err)
		}
	})
	if download.Size != int64(len(data)) || len(download.SHA256) != 64 {
		t.Fatalf("invalid pin: %+v", download)
	}
	directory, err := ExtractZIP(t.Context(), download.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	contents, err := os.ReadFile(filepath.Join(directory, "nested/app.txt"))
	if err != nil || string(contents) != "package contents" {
		t.Fatalf("contents=%q err=%v", contents, err)
	}
	info, err := os.Stat(filepath.Join(directory, "nested/app.txt"))
	if err != nil || info.Mode().Perm()&0111 != 0 {
		t.Fatalf("executable bits preserved: %v %v", info, err)
	}
}

func TestFetchRejectsRepublishedZIP(t *testing.T) {
	// Given
	data := zipFixture(t, "app.txt", 0644)
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := w.Write(data); err != nil {
				t.Error(err)
			}
		}),
	)
	defer server.Close()
	client := &Client{HTTP: server.Client()}
	source := Repository{
		URLTemplate: server.URL + "/{version}.zip",
		AuthType:    "noauth",
	}
	// When
	_, err := client.Fetch(
		t.Context(),
		source,
		Pin{
			URL:    server.URL + "/1.zip",
			SHA256: "incorrect",
			Size:   int64(len(data)),
		},
	)
	// Then
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("expected checksum error, got %v", err)
	}
}

func TestRepositoryRejectsUnsafeTemplates(t *testing.T) {
	for _, raw := range []string{"http://example.com/{version}", "https://{version}.example.com/a", "https://user:secret@example.com/{version}", "https://example.com/a?v={version}", "https://example.com/static.zip"} {
		t.Run(raw, func(t *testing.T) {
			// Given / When
			err := (Repository{URLTemplate: raw, AuthType: "noauth"}).Validate()
			// Then
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted unsafe URL: %v", err)
			}
		})
	}
}

func TestRepositoryDialRejectsLoopback(t *testing.T) {
	// Given / When
	_, err := dialRepository(context.Background(), "tcp", "127.0.0.1:443")
	// Then
	if !errors.Is(err, ErrFetch) {
		t.Fatalf("expected blocked address, got %v", err)
	}
}
