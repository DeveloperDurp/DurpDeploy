package staging

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
)

// Merge applies changes relative to the same base, including deletions.
// Identical changes coalesce; conflicting changes never choose a winner.
func Merge(base Snapshot, outputs []Snapshot) (Snapshot, error) {
	result := Snapshot{Entries: make(map[string]Entry, len(base.Entries))}
	for name, entry := range base.Entries {
		result.Entries[name] = entry
	}
	changed := make(map[string]bool)
	for _, output := range outputs {
		names := make(map[string]bool, len(base.Entries)+len(output.Entries))
		for name := range base.Entries {
			names[name] = true
		}
		for name := range output.Entries {
			names[name] = true
		}
		for name := range names {
			original, existed := base.Entries[name]
			candidate, exists := output.Entries[name]
			if existed == exists && (!exists || original.same(candidate)) {
				continue
			}
			if changed[name] {
				prior, present := result.Entries[name]
				if present != exists || exists && !prior.same(candidate) {
					return Snapshot{}, fmt.Errorf(
						"path %q: %w",
						name,
						ErrConflict,
					)
				}
			}
			changed[name] = true
			if exists {
				result.Entries[name] = candidate
			} else {
				delete(result.Entries, name)
			}
		}
	}
	var total int64
	for name, entry := range result.Entries {
		total += entry.Size
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			ancestor, exists := result.Entries[parent]
			if !exists || !ancestor.Directory {
				return Snapshot{}, fmt.Errorf("path %q: %w", name, ErrConflict)
			}
		}
	}
	if total > MaxBytes || len(result.Entries) > MaxFiles {
		return Snapshot{}, ErrInvalid
	}
	return result, nil
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
