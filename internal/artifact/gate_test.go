package artifact

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestGateSpoolHasNoNamedSensitiveFile(t *testing.T) {
	file, err := OpenGateSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := os.Stat(file.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("named spool remains: %v", err)
	}
	if _, err := file.WriteString("sensitive"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "sensitive" {
		t.Fatal("spool lost content")
	}
}

func TestGateReviewNeverExposesTerraformValues(t *testing.T) {
	// Given: a plan containing nested sensitive values and resource names.
	raw := []byte(
		`{"format_version":"1.2","resource_changes":[{"address":"secret-name","change":{"actions":["delete","create"],"after":{"value":"secret"}}}],"output_changes":{"password":"secret"}}`,
	)
	// When: its review representation is parsed.
	review, err := ParseGateReview(raw, "terraform")
	// Then: only the action counts survive.
	if err != nil || review != (GateReview{Create: 1, Delete: 1}) {
		t.Fatalf("review=%+v err=%v", review, err)
	}
}

func TestGateBundleRejectsUnsafeEntries(t *testing.T) {
	for _, header := range []tar.Header{
		{Name: "../plan", Typeflag: tar.TypeReg},
		{Name: "plan", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
		{Name: "plan", Typeflag: tar.TypeLink, Linkname: "other"},
		{Name: "plan", Typeflag: tar.TypeReg, Size: MaxDownload + 1},
	} {
		t.Run(header.Name+string(header.Typeflag), func(t *testing.T) {
			// Given: an untrusted archive header.
			var data bytes.Buffer
			writer := tar.NewWriter(&data)
			if err := writer.WriteHeader(&header); err != nil {
				t.Fatal(err)
			}
			// When: the generated bundle is captured.
			_, _, err := CopyGateBundle(
				context.Background(),
				&data,
				io.Discard,
				"plan",
				"review",
				"summary",
			)
			// Then: publication is refused.
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestGateConfigRejectsInvalidNetworkAndPaths(t *testing.T) {
	for _, input := range [][5]string{
		{"local", "host", "", "", ""},
		{"agent", "bridge", "", "", ""},
		{"agent", "", "plan", "review", "summary"},
		{"local", "bridge", "../plan", "review", "summary"},
		{"local", "bridge", "plan", "plan", "summary"},
		{"local", "bridge", "plan", "review", ""},
	} {
		// Given: invalid HTTP configuration.
		// When: it is parsed at the boundary.
		err := ValidateGateConfig(
			input[0],
			input[1],
			input[2],
			input[3],
			input[4],
		)
		// Then: unsafe network/path combinations are rejected.
		if !errors.Is(err, ErrGateConfig) {
			t.Fatalf("input=%v err=%v", input, err)
		}
	}
}
