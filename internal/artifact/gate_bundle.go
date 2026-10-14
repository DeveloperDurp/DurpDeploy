package artifact

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// CopyGateBundle rejects links, devices, traversal, duplicate entries and
// excessive entry counts. Canonical metadata prevents container-supplied ownership.
func CopyGateBundle(
	ctx context.Context,
	source io.Reader,
	target io.Writer,
	artifactPath, reviewPath, format string,
) (string, GateReview, error) {
	reader := tar.NewReader(source)
	writer := tar.NewWriter(target)
	var result GateReview
	var checksum string
	seen := make(map[string]bool)
	for {
		if err := ctx.Err(); err != nil {
			return "", result, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", result, ErrInvalid
		}
		name := strings.TrimSuffix(strings.TrimPrefix(header.Name, "./"), "/")
		if name == "." || name == "" {
			continue
		}
		if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") ||
			seen[name] ||
			len(seen) >= MaxStagingNodes {
			return "", result, ErrInvalid
		}
		seen[name] = true
		if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
			return "", result, fmt.Errorf(
				"approval context contains a link or special file: %w",
				ErrInvalid,
			)
		}
		if header.Size < 0 {
			return "", result, ErrInvalid
		}
		canonical := &tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     header.Size,
			Typeflag: header.Typeflag,
			Uid:      65534,
			Gid:      65534,
		}
		if header.Typeflag == tar.TypeReg && header.Mode&0111 != 0 {
			canonical.Mode = 0755
		}
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return "", result, ErrInvalid
			}
			canonical.Mode = 0755
			canonical.Size = 0
		}
		if err := writer.WriteHeader(canonical); err != nil {
			return "", result, err
		}
		var content io.Reader = reader
		if name == reviewPath {
			if header.Typeflag != tar.TypeReg {
				return "", result, fmt.Errorf(
					"approval review must be a regular file: %w", ErrInvalid)
			}
			raw, err := io.ReadAll(reader)
			if err != nil {
				return "", result, err
			}
			result, err = ParseGateReview(raw, format)
			if err != nil {
				return "", result, fmt.Errorf(
					"parse approval review JSON: %w",
					err,
				)
			}
			content = strings.NewReader(string(raw))
		}
		if name == artifactPath {
			if header.Typeflag != tar.TypeReg || header.Size == 0 {
				return "", result, fmt.Errorf(
					"approval artifact must be a nonempty regular file: %w",
					ErrInvalid,
				)
			}
			hash := sha256.New()
			if _, err := io.Copy(
				writer,
				io.TeeReader(content, hash),
			); err != nil {
				return "", result, err
			}
			checksum = hex.EncodeToString(hash.Sum(nil))
		} else if _, err := io.Copy(writer, content); err != nil {
			return "", result, err
		}
	}
	if checksum == "" {
		return "", result, fmt.Errorf(
			"approval artifact file is missing: %w",
			ErrInvalid,
		)
	}
	if !seen[reviewPath] {
		return "", result, fmt.Errorf(
			"approval review file is missing: %w",
			ErrInvalid,
		)
	}
	return checksum, result, writer.Close()
}
