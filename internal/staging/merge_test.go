package staging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/testutil"
)

func newMergeFixture(t *testing.T, base Snapshot) (*Merger, *os.File) {
	t.Helper()
	file, err := artifact.OpenGateSpool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, file) })
	merger, err := NewMerger(t.Context(), base, file)
	if err != nil {
		t.Fatal(err)
	}
	return merger, file
}

func mergeFixture(
	t *testing.T,
	base Snapshot,
	outputs []Snapshot,
) (Snapshot, error) {
	t.Helper()
	m, _ := newMergeFixture(t, base)
	for _, output := range outputs {
		if err := m.Add(t.Context(), output); err != nil {
			return Snapshot{}, err
		}
	}
	return m.Finish(t.Context())
}

func TestMergerOwnsChangedContentAndCoalesces(t *testing.T) {
	base := snapshotFixture(
		t,
		map[string]string{"stable": "original", "delete": "gone"},
	)
	output := snapshotFixture(
		t,
		map[string]string{"stable": "original", "new": "changed"},
	)
	m, content := newMergeFixture(t, base)
	if err := m.Add(t.Context(), output); err != nil {
		t.Fatal(err)
	}
	// The next agent repeats the same edit from its own immutable workspace.
	if err := m.Add(t.Context(), snapshotFixture(t, map[string]string{"stable": "original", "new": "changed"})); err != nil {
		t.Fatal(err)
	}
	info, err := content.Stat()
	if err != nil || info.Size() != int64(len("changed")) {
		t.Fatalf("stored duplicate changes: %v %v", info, err)
	}
	file := output.Entries["new"].file
	// Destroy the source bytes after Add. The result must use the owned copy.
	if err := file.Truncate(0); err != nil {
		t.Fatal(err)
	}
	merged, err := m.Finish(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := merged.Write(t.Context(), &buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("changed")) ||
		!bytes.Contains(buf.Bytes(), []byte("original")) {
		t.Fatal("result lost source content")
	}
	if _, exists := merged.Entries["delete"]; exists {
		t.Fatal("deletion lost")
	}
}

func TestMergerEntryWorkBoundary(t *testing.T) {
	// 20,000 initial visits + four pairs of 20,000 = 180,000.
	base := Snapshot{Entries: make(map[string]Entry, MaxFiles)}
	for i := range MaxFiles {
		base.Entries[fmt.Sprint(i)] = Entry{}
	}
	m, _ := newMergeFixture(t, base)
	for range 4 {
		if err := m.Add(t.Context(), base); err != nil {
			t.Fatal(err)
		}
	}
	// Empty output uses the last 20,000 visits for same-base deletions.
	if err := m.Add(t.Context(), Snapshot{}); err != nil {
		t.Fatalf("exact budget: %v", err)
	}
	if _, err := m.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(t.Context(), Snapshot{}); !errors.Is(err, ErrMergeBudget) {
		t.Fatalf("excess work: %v", err)
	}
}

func TestMergerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewMerger(ctx, Snapshot{}, nil); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("new: %v", err)
	}
	m, _ := newMergeFixture(t, Snapshot{})
	if err := m.Add(ctx, Snapshot{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("add: %v", err)
	}
	if _, err := m.Finish(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("finish: %v", err)
	}
}

func TestMergerAllowsLaterBaseDeletions(t *testing.T) {
	base := Snapshot{Entries: make(map[string]Entry, MaxFiles)}
	for i := range MaxFiles {
		base.Entries[fmt.Sprint(i)] = Entry{}
	}
	left := Snapshot{Entries: make(map[string]Entry, MaxFiles)}
	for name, entry := range base.Entries {
		if name != "0" {
			left.Entries[name] = entry
		}
	}
	left.Entries["new-left"] = Entry{}
	right := Snapshot{Entries: make(map[string]Entry, MaxFiles)}
	for name, entry := range base.Entries {
		if name != "0" {
			right.Entries[name] = entry
		}
	}
	right.Entries["new-right"] = Entry{}
	last := Snapshot{Entries: make(map[string]Entry, MaxFiles)}
	for name, entry := range base.Entries {
		if name != "1" {
			last.Entries[name] = entry
		}
	}
	merged, err := mergeFixture(t, base, []Snapshot{left, right, last})
	if err != nil || len(merged.Entries) != MaxFiles {
		t.Fatalf("final tree: %d %v", len(merged.Entries), err)
	}
}

func TestMergerRejectsFinalLimits(t *testing.T) {
	for _, scenario := range []string{"bytes", "paths"} {
		t.Run(scenario, func(t *testing.T) {
			base := mergeFinalLimitBase(scenario)
			m, _ := newMergeFixture(t, base)
			output := Snapshot{Entries: make(map[string]Entry)}
			for name, entry := range base.Entries {
				output.Entries[name] = entry
			}
			output.Entries["extra"] = Entry{Directory: true}
			if scenario == "bytes" {
				// Synthetic metadata exercises final arithmetic without allocating 512 MiB.
				// A real backing file keeps the copied byte meaningful.
				entry := snapshotFixture(t, map[string]string{"extra": "x"}).Entries["extra"]
				output.Entries["extra"] = entry
			}
			if err := m.Add(t.Context(), output); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Finish(t.Context()); !errors.Is(err, ErrInvalid) {
				t.Fatalf("final limit: %v", err)
			}
		})
	}
}

func mergeFinalLimitBase(scenario string) Snapshot {
	base := Snapshot{
		Entries: map[string]Entry{"base": {Size: MaxBytes}},
	}
	if scenario == "paths" {
		base.Entries = make(map[string]Entry, MaxFiles)
		for i := range MaxFiles {
			base.Entries[fmt.Sprint(i)] = Entry{}
		}
	}
	return base
}
