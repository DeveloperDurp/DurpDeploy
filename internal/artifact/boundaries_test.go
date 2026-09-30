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
	"strings"
	"testing"
)

func TestFetchRejectsCrossOriginRedirect(t *testing.T) {
	// Given
	var leaked bool
	destination := httptest.NewTLSServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { leaked = true },
		),
	)
	defer destination.Close()
	source := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, destination.URL+"/secret", http.StatusFound)
		}),
	)
	defer source.Close()
	client := &Client{HTTP: source.Client()}
	repository := Repository{
		URLTemplate: source.URL + "/{version}.zip",
		AuthType:    "bearer",
		Credential:  "secret",
	}
	// When
	_, err := client.Fetch(
		t.Context(),
		repository,
		Pin{URL: source.URL + "/1.zip"},
	)
	// Then
	if !errors.Is(err, ErrFetch) || leaked {
		t.Fatalf("redirect error=%v leaked=%v", err, leaked)
	}
}

func TestValidateZIPRejectsSizeAndTreeCollisions(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		entries []zip.FileHeader
	}{
		{"declared bomb", []zip.FileHeader{{Name: "bomb", UncompressedSize64: uint64(MaxExtracted + 1)}}},
		{"file then child", []zip.FileHeader{{Name: "a"}, {Name: "a/b"}}},
		{"child then file", []zip.FileHeader{{Name: "a/b"}, {Name: "a"}}},
		{"duplicate", []zip.FileHeader{{Name: "a"}, {Name: "a"}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// Given
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			for _, header := range fixture.entries {
				if _, err := writer.CreateRaw(&header); err != nil {
					t.Fatal(err)
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
			err := ValidateZIP(t.Context(), filename)
			// Then
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid ZIP: %v", err)
			}
		})
	}
}

func TestEntryHonorsCancellationDuringRead(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(t.Context())
	reader := contextReader{ctx: ctx, reader: strings.NewReader("contents")}
	cancel()
	// When
	_, err := reader.Read(make([]byte, 8))
	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
