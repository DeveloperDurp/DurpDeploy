package artifact

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	kinds := make(map[string]bool, len(archive.File))
	var total int64
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateZIPEntry(entry, kinds); err != nil {
			return err
		}
		if entry.UncompressedSize64 > uint64(MaxExtracted-total) {
			return ErrInvalid
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
	if entry.Mode().IsDir() {
		if directory == "" {
			return 0, nil
		}
		return 0, os.MkdirAll(filepath.Join(directory, entry.Name), 0755)
	}
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
	ctx    context.Context // NOSONAR: short-lived io.Reader adapter; Read cannot accept a context.
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
