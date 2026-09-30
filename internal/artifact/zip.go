package artifact

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func ValidateZIP(ctx context.Context, filename string) error {
	return processZIP(ctx, filename, "")
}

// ExtractZIP writes regular files only into a newly created private directory.
// Mount this directory's contents read-only/noexec before exposing them to steps.
func ExtractZIP(
	ctx context.Context,
	filename string,
) (directory string, err error) {
	directory, err = os.MkdirTemp("", "durpdeploy-artifact-")
	if err != nil {
		return "", err
	}
	if err = processZIP(ctx, filename, directory); err != nil {
		return "", errors.Join(err, os.RemoveAll(directory))
	}
	return directory, nil
}

func processZIP(ctx context.Context, filename, directory string) (err error) {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return ErrInvalid
	}
	defer func() { err = errors.Join(err, archive.Close()) }()
	if len(archive.File) == 0 || len(archive.File) > MaxFiles {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(archive.File))
	kinds := make(map[string]bool, len(archive.File))
	var total int64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if !fs.ValidPath(name) || path.Clean(name) != name ||
			strings.ContainsAny(name, "\\:\x00") ||
			seen[name] {
			return ErrInvalid
		}
		seen[name] = true
		mode := entry.Mode()
		if !mode.IsRegular() && !mode.IsDir() {
			return ErrInvalid
		}
		if directoryEntry, exists := kinds[name]; exists &&
			(!directoryEntry || !mode.IsDir()) {
			return ErrInvalid
		}
		kinds[name] = mode.IsDir()
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if directoryEntry, exists := kinds[parent]; exists &&
				!directoryEntry {
				return ErrInvalid
			}
			kinds[parent] = true
		}
		if entry.UncompressedSize64 > uint64(MaxExtracted-total) {
			return ErrInvalid
		}
		if mode.IsDir() {
			if directory != "" {
				if err := os.MkdirAll(
					filepath.Join(directory, name),
					0755,
				); err != nil {
					return err
				}
			}
			continue
		}
		count, err := processEntry(ctx, entry, directory, MaxExtracted-total)
		if err != nil {
			return err
		}
		total += count
	}
	return nil
}

func processEntry(
	ctx context.Context,
	entry *zip.File,
	directory string,
	remaining int64,
) (size int64, err error) {
	reader, err := entry.Open()
	if err != nil {
		return 0, ErrInvalid
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	var target io.Writer = io.Discard
	if directory != "" {
		filename := filepath.Join(directory, entry.Name)
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			return 0, err
		}
		file, err := os.OpenFile(
			filename,
			os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			0644,
		)
		if err != nil {
			return 0, err
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		target = file
	}
	size, err = io.Copy(
		target,
		io.LimitReader(contextReader{ctx, reader}, remaining+1),
	)
	if ctx.Err() != nil {
		return size, ctx.Err()
	}
	if err != nil || size > remaining {
		return size, ErrInvalid
	}
	return size, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
