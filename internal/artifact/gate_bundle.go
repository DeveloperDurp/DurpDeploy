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

	"durpdeploy/internal/config"
)

// ValidateGateBundleNodes checks an authenticated saved context before restore.
// Capture already canonicalizes its entries and removes the root header.
func ValidateGateBundleNodes(
	ctx context.Context,
	source io.Reader,
	limit int64,
) error {
	reader := tar.NewReader(source)
	for count := int64(0); ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return ErrInvalid
		}
		if count >= limit {
			return fmt.Errorf(
				"%w: approval context exceeds staging-node limit",
				ErrInvalid,
			)
		}
	}
}

// CopyGateBundle rejects links, devices, traversal, duplicate entries and
// excessive entry counts. Canonical metadata prevents container-supplied ownership.
func CopyGateBundle(
	ctx context.Context,
	source io.Reader,
	target io.Writer,
	artifactPath, reviewPath, format string,
) (string, GateReview, error) {
	return CopyGateBundleWithLimits(
		ctx,
		source,
		target,
		artifactPath,
		reviewPath,
		format,
		config.Defaults().Limits.Artifact,
	)
}

// CopyGateBundleWithLimits applies the captured node budget to approval contexts.
func CopyGateBundleWithLimits(
	ctx context.Context,
	source io.Reader,
	target io.Writer,
	artifactPath, reviewPath, format string,
	limits config.ArtifactLimits,
) (string, GateReview, error) {
	reader := tar.NewReader(source)
	bundle := gateBundleWriter{
		writer:       tar.NewWriter(target),
		artifactPath: artifactPath,
		reviewPath:   reviewPath,
		format:       format,
	}
	seen := make(map[string]bool)
	for {
		if err := ctx.Err(); err != nil {
			return "", bundle.review, err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", bundle.review, ErrInvalid
		}
		canonical, err := canonicalGateHeader(header, seen, limits.StagingNodes)
		if err != nil {
			return "", bundle.review, err
		}
		if canonical == nil {
			continue
		}
		if err := bundle.writeEntry(reader, canonical); err != nil {
			return "", bundle.review, err
		}
	}
	if bundle.checksum == "" {
		return "", bundle.review, fmt.Errorf(
			"approval artifact file is missing: %w",
			ErrInvalid,
		)
	}
	if !seen[reviewPath] {
		return "", bundle.review, fmt.Errorf(
			"approval review file is missing: %w",
			ErrInvalid,
		)
	}
	return bundle.checksum, bundle.review, bundle.writer.Close()
}

func canonicalGateHeader(
	header *tar.Header,
	seen map[string]bool,
	limit int64,
) (*tar.Header, error) {
	name := strings.TrimSuffix(strings.TrimPrefix(header.Name, "./"), "/")
	if name == "." || name == "" {
		return nil, nil
	}
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") ||
		seen[name] || int64(len(seen)) >= limit {
		return nil, ErrInvalid
	}
	seen[name] = true
	if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf(
			"approval context contains a link or special file: %w", ErrInvalid,
		)
	}
	if header.Size < 0 {
		return nil, ErrInvalid
	}
	canonical := &tar.Header{
		Name: name, Mode: 0644, Size: header.Size,
		Typeflag: header.Typeflag, Uid: 65534, Gid: 65534,
	}
	if header.Typeflag == tar.TypeReg && header.Mode&0111 != 0 {
		canonical.Mode = 0755
	}
	if header.Typeflag == tar.TypeDir {
		if header.Size != 0 {
			return nil, ErrInvalid
		}
		canonical.Mode = 0755
		canonical.Size = 0
	}
	return canonical, nil
}

type gateBundleWriter struct {
	writer       *tar.Writer
	artifactPath string
	reviewPath   string
	format       string
	checksum     string
	review       GateReview
}

func (b *gateBundleWriter) writeEntry(
	source io.Reader,
	header *tar.Header,
) error {
	if err := b.writer.WriteHeader(header); err != nil {
		return err
	}
	if header.Name == b.reviewPath {
		if header.Typeflag != tar.TypeReg {
			return fmt.Errorf(
				"approval review must be a regular file: %w",
				ErrInvalid,
			)
		}
		var err error
		b.review, err = ParseGateReviewReader(
			io.TeeReader(source, b.writer),
			b.format,
		)
		if err != nil {
			return fmt.Errorf("parse approval review JSON: %w", err)
		}
		return nil
	}
	if header.Name == b.artifactPath {
		if header.Typeflag != tar.TypeReg || header.Size == 0 {
			return fmt.Errorf(
				"approval artifact must be a nonempty regular file: %w",
				ErrInvalid,
			)
		}
		hash := sha256.New()
		if _, err := io.Copy(b.writer, io.TeeReader(source, hash)); err != nil {
			return err
		}
		b.checksum = hex.EncodeToString(hash.Sum(nil))
		return nil
	}
	_, err := io.Copy(b.writer, source)
	return err
}
