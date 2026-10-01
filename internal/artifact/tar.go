package artifact

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteTar transports only the validated tree, with server-controlled metadata.
func WriteTar(
	ctx context.Context,
	directory string,
	target io.Writer,
) (err error) {
	writer := tar.NewWriter(target)
	defer func() { err = errors.Join(err, writer.Close()) }()
	return filepath.WalkDir(
		directory,
		func(filename string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if filename == directory {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() && !info.IsDir() {
				return ErrInvalid
			}
			name, err := filepath.Rel(directory, filename)
			if err != nil {
				return err
			}
			header := &tar.Header{
				Name:     filepath.ToSlash(name),
				Mode:     0644,
				Size:     info.Size(),
				Typeflag: tar.TypeReg,
				Uid:      65534,
				Gid:      65534,
			}
			if info.IsDir() {
				header.Mode = 0755
				header.Size = 0
				header.Typeflag = tar.TypeDir
			}
			if err := writer.WriteHeader(header); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			file, err := os.Open(filename)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(writer, contextReader{ctx, file})
			return errors.Join(copyErr, file.Close())
		},
	)
}
