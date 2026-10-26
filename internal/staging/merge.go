package staging

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"

	"durpdeploy/internal/config"
)

const (
	MaxMergeArchiveBytes int64 = 2 << 30
	MaxMergeEntryVisits        = 200000
	MaxMergeOutputs            = 64
)

var ErrMergeBudget = errors.New("staging merge budget exceeded")

// Merger compares every output with one immutable base. Changed file bytes
// move into content so the caller can close each output immediately after Add.
// The caller owns both the base file and content until the final Write ends.
type Merger struct {
	limits      config.StagingLimits
	base        Snapshot
	result      Snapshot
	changed     map[string]bool
	content     *os.File
	remaining   int64
	stored      int64
	storedPaths int
}

// NewMerger uses content as an empty append-only spool.
func NewMerger(
	ctx context.Context,
	base Snapshot,
	content *os.File,
) (*Merger, error) {
	return NewMergerWithLimits(
		ctx,
		base,
		content,
		config.Defaults().Limits.Staging,
		config.Defaults().Limits.Merge,
	)
}

// NewMergerWithLimits applies captured staging and entry-visit budgets.
func NewMergerWithLimits(
	ctx context.Context,
	base Snapshot,
	content *os.File,
	limits config.StagingLimits,
	budget config.MergeLimits,
) (*Merger, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(base.Entries)) > budget.EntryVisits {
		return nil, ErrMergeBudget
	}
	m := &Merger{
		limits:  limits,
		base:    base,
		result:  Snapshot{Entries: make(map[string]Entry, len(base.Entries))},
		changed: make(map[string]bool), content: content,
		remaining: budget.EntryVisits - int64(len(base.Entries)),
	}
	for name, entry := range base.Entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.result.Entries[name] = entry
	}
	return m, nil
}

func (m *Merger) Add(ctx context.Context, output Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	baseVisits, outputVisits := int64(
		len(m.base.Entries),
	), int64(
		len(output.Entries),
	)
	if baseVisits > m.remaining || outputVisits > m.remaining-baseVisits {
		return ErrMergeBudget
	}
	m.remaining -= baseVisits + outputVisits
	for name := range m.base.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, exists := output.Entries[name]; !exists {
			if err := m.change(ctx, name, Entry{}, false); err != nil {
				return err
			}
		}
	}
	for name, candidate := range output.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if original, exists := m.base.Entries[name]; exists &&
			original.same(candidate) {
			continue
		}
		if err := m.change(ctx, name, candidate, true); err != nil {
			return err
		}
	}
	return nil
}

func (m *Merger) change(
	ctx context.Context,
	name string,
	candidate Entry,
	exists bool,
) error {
	if m.changed[name] {
		prior, present := m.result.Entries[name]
		if present != exists || exists && !prior.same(candidate) {
			return fmt.Errorf("path %q: %w", name, ErrConflict)
		}
		return nil
	}
	if !exists {
		delete(m.result.Entries, name)
	} else {
		// Accepted changes cannot be removed by another output without conflict.
		if candidate.Size > m.limits.PayloadBytes-m.stored ||
			int64(m.storedPaths) >= m.limits.Entries {
			return ErrInvalid
		}
		if !candidate.Directory {
			if candidate.Size > 0 {
				reader := &boundedReader{ctx: ctx, reader: io.NewSectionReader(candidate.file, candidate.offset, candidate.Size), remaining: candidate.Size}
				if _, err := io.CopyN(m.content, reader, candidate.Size); err != nil {
					return err
				}
			}
			candidate.file, candidate.offset = m.content, m.stored
			m.stored += candidate.Size
		}
		m.storedPaths++
		m.result.Entries[name] = candidate
	}
	m.changed[name] = true
	return nil
}

// Finish validates the final tree after all same-base changes are applied.
func (m *Merger) Finish(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	var total int64
	for name, entry := range m.result.Entries {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if entry.Size > m.limits.PayloadBytes-total {
			return Snapshot{}, ErrInvalid
		}
		total += entry.Size
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			ancestor, exists := m.result.Entries[parent]
			if !exists || !ancestor.Directory {
				return Snapshot{}, fmt.Errorf("path %q: %w", name, ErrConflict)
			}
		}
	}
	if int64(len(m.result.Entries)) > m.limits.Entries {
		return Snapshot{}, ErrInvalid
	}
	return m.result, nil
}

// Write emits canonical metadata and streams file bytes without extracting them.
func (s Snapshot) Write(ctx context.Context, target io.Writer) (err error) {
	writer := tar.NewWriter(target)
	defer func() { err = errors.Join(err, writer.Close()) }()
	names := make([]string, 0, len(s.Entries))
	for name := range s.Entries {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := s.Entries[name]
		header := &tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     entry.Size,
			Typeflag: tar.TypeReg,
			Uid:      65534,
			Gid:      65534,
		}
		if entry.Directory {
			header.Mode = 0755
			header.Typeflag = tar.TypeDir
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if !entry.Directory {
			reader := &boundedReader{
				ctx: ctx,
				reader: io.NewSectionReader(
					entry.file,
					entry.offset,
					entry.Size,
				),
				remaining: entry.Size,
			}
			if entry.Size > 0 {
				if _, err := io.CopyN(writer, reader, entry.Size); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
