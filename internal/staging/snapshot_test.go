package staging

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func snapshotFixture(t *testing.T, files map[string]string) Snapshot {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stage")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	writer := tar.NewWriter(file)
	for name, data := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Read(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSnapshotMergeCombinesChangesAndDeletions(t *testing.T) {
	base := snapshotFixture(
		t,
		map[string]string{
			"shared":     "base",
			"remove":     "old",
			"dir/stable": "keep",
		},
	)
	left := snapshotFixture(
		t,
		map[string]string{
			"shared":     "new",
			"dir/stable": "keep",
			"dir/left":   "left",
		},
	)
	right := snapshotFixture(
		t,
		map[string]string{
			"shared":     "new",
			"remove":     "old",
			"dir/stable": "keep",
			"right":      "right",
		},
	)
	merged, err := Merge(base, []Snapshot{left, right})
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := merged.Write(t.Context(), &buffer); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&buffer)
	actual := make(map[string]string)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		actual[header.Name] = string(data)
	}
	if len(actual) != 4 || actual["shared"] != "new" ||
		actual["dir/stable"] != "keep" ||
		actual["dir/left"] != "left" ||
		actual["right"] != "right" {
		t.Fatalf("merged files = %v", actual)
	}
}

func TestSnapshotMergeRejectsConflicts(t *testing.T) {
	for _, scenario := range []struct {
		name              string
		base, left, right map[string]string
	}{
		{"different writes", nil, map[string]string{"file": "left"}, map[string]string{"file": "right"}},
		{"delete versus change", map[string]string{"file": "base"}, nil, map[string]string{"file": "right"}},
		{"ancestor deletion", map[string]string{"dir/file": "base"}, nil, map[string]string{"dir/file": "base", "dir/new": "right"}},
		{"file versus directory", nil, map[string]string{"dir": "file"}, map[string]string{"dir/file": "right"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, err := Merge(
				snapshotFixture(t, scenario.base),
				[]Snapshot{
					snapshotFixture(t, scenario.left),
					snapshotFixture(t, scenario.right),
				},
			)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("conflict error = %v", err)
			}
		})
	}
}

func TestSnapshotRejectsUnsafeArchives(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		headers  []*tar.Header
		trailing bool
	}{
		{"traversal", []*tar.Header{{Name: "../escape", Typeflag: tar.TypeReg}}, false},
		{"absolute", []*tar.Header{{Name: "/escape", Typeflag: tar.TypeReg}}, false},
		{"symlink", []*tar.Header{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}, false},
		{"duplicate", []*tar.Header{{Name: "file", Typeflag: tar.TypeReg}, {Name: "file", Typeflag: tar.TypeReg}}, false},
		{"ancestor file", []*tar.Header{{Name: "dir/file", Typeflag: tar.TypeReg}, {Name: "dir", Typeflag: tar.TypeReg}}, false},
		{"oversized", []*tar.Header{{Name: "file", Typeflag: tar.TypeReg, Size: MaxBytes + 1}}, false},
		{"trailing payload", nil, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "archive")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			writer := tar.NewWriter(file)
			for _, header := range scenario.headers {
				if err := writer.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
			}
			closeErr := writer.Close()
			if scenario.name != "oversized" && closeErr != nil {
				t.Fatal(closeErr)
			}
			if scenario.trailing {
				if _, err := file.WriteString("hidden"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Read(t.Context(), file); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSnapshotHonorsCancellation(t *testing.T) {
	fixture := snapshotFixture(t, map[string]string{"file": "data"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := fixture.Write(ctx, io.Discard); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("write error = %v", err)
	}
}
