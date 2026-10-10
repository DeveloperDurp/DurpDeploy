package repository

import (
	"fmt"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

// PackageMissingError retains the source identity for the creation transaction.
// It exposes only the release version in user-facing errors.
type PackageMissingError struct {
	Version string
	Source  db.PackageRepository
}

func (e *PackageMissingError) Error() string {
	return fmt.Sprintf("Package version %q was not found in this project's "+
		"package repository (HTTP 404). The release version selects the "+
		"package to download and validate. Publish the package before "+
		"refreshing the release checksum or deploying it.",
		e.Version)
}

func (e *PackageMissingError) Unwrap() error { return artifact.ErrNotFound }
