package artifact

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type gateArchiveEntry struct {
	name, content string
	kind          byte
}

func gateArchive(t *testing.T, entries []gateArchiveEntry) *bytes.Buffer {
	t.Helper()
	var source bytes.Buffer
	writer := tar.NewWriter(&source)
	for _, entry := range entries {
		if err := writer.WriteHeader(&tar.Header{
			Name: entry.name, Size: int64(len(entry.content)),
			Typeflag: entry.kind, Mode: 0600,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &source
}

func TestGateBundleRejectsMissingAndInvalidFiles(t *testing.T) {
	plan := gateArchiveEntry{"plan", "exact-plan", tar.TypeReg}
	review := gateArchiveEntry{"review", `{"create":1}`, tar.TypeReg}
	for _, entries := range [][]gateArchiveEntry{
		{review}, {plan},
		{plan, {"review", "", tar.TypeDir}},
		{{"plan", "", tar.TypeDir}, review},
		{{"plan", "", tar.TypeReg}, review},
		{plan, {"review", `{"create":null}`, tar.TypeReg}},
		{plan, review, review},
	} {
		_, _, err := CopyGateBundle(
			t.Context(),
			gateArchive(t, entries),
			io.Discard,
			"plan",
			"review",
			"summary",
		)
		if err == nil {
			t.Fatalf("accepted invalid files: %+v", entries)
		}
	}
}

type gateFailingWriter struct{ remaining int }

func (w *gateFailingWriter) Write(p []byte) (int, error) {
	n := min(w.remaining, len(p))
	w.remaining -= n
	if n < len(p) {
		return n, io.ErrClosedPipe
	}
	return n, nil
}

func TestGateBundlePropagatesWriterFailuresAndCancellation(t *testing.T) {
	entries := []gateArchiveEntry{
		{"review", `{"create":1}`, tar.TypeReg},
		{"plan", "exact-plan", tar.TypeReg},
	}
	for _, remaining := range []int{0, 514, 1538} {
		_, _, err := CopyGateBundle(
			t.Context(),
			gateArchive(t, entries),
			&gateFailingWriter{remaining},
			"plan",
			"review",
			"summary",
		)
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("remaining=%d err=%v", remaining, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := CopyGateBundle(
		ctx,
		gateArchive(t, entries),
		io.Discard,
		"plan",
		"review",
		"summary",
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture err=%v", err)
	}
}
