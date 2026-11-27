package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

type artifactPreparationCall struct {
	name string
	run  func(context.Context, int64, string) (*repository.ArtifactSnapshot, error)
}

func artifactPreparationCalls(
	r *repository.Repository,
) []artifactPreparationCall {
	return []artifactPreparationCall{
		{"resolve", r.ResolveProjectArtifact},
		{"capture", r.CaptureProjectArtifact},
		{"test", func(ctx context.Context, id int64, version string) (
			*repository.ArtifactSnapshot, error,
		) {
			_, err := r.TestProjectPackage(ctx, id, version)
			return nil, err
		}},
	}
}

// Keep the attachment query real while failing only the later source lookup.
type artifactPreparationQuery struct {
	db.DBTX
	queryName string
	noRows    bool
}

func (q artifactPreparationQuery) QueryRowContext(
	ctx context.Context, query string, args ...interface{},
) *sql.Row {
	if strings.HasPrefix(query, "-- name: "+q.queryName+" :one\n") {
		if q.noRows {
			return q.DBTX.QueryRowContext(ctx, query, int64(-1))
		}
		return q.DBTX.QueryRowContext(ctx, "SELECT * FROM unavailable_source")
	}
	return q.DBTX.QueryRowContext(ctx, query, args...)
}

func TestArtifactPreparationErrors(t *testing.T) {
	for _, caller := range []string{"resolve", "capture", "test"} {
		for _, failure := range []string{
			"absent", "attachment-query", "source-missing", "source-query",
			"credential", "version", "url",
		} {
			t.Run(caller+"/"+failure, func(t *testing.T) {
				testArtifactPreparationError(t, caller, failure)
			})
		}
	}
}

func testArtifactPreparationError(t *testing.T, caller, failure string) {
	t.Helper()
	f := newArtifactFixture(t)
	version := "1.2.3"
	switch failure {
	case "absent":
		err := f.repo.Queries.DeleteProjectArtifactRepository(
			t.Context(), f.project.ID,
		)
		require.NoError(t, err)
	case "attachment-query", "source-missing", "source-query":
		queryName := "GetPackageRepository"
		if failure == "attachment-query" {
			queryName = "GetProjectArtifactRepository"
		}
		f.repo.Queries = db.New(artifactPreparationQuery{
			DBTX: f.repo.DB, queryName: queryName,
			noRows: failure == "source-missing",
		})
	case "credential":
		_, err := f.repo.DB.ExecContext(
			t.Context(),
			"UPDATE package_repositories SET credential = ? WHERE id = ?",
			"invalid-ciphertext",
			f.source.ID,
		)
		require.NoError(t, err)
	case "version":
		version = "../invalid"
	case "url":
		_, err := f.repo.DB.ExecContext(
			t.Context(),
			"UPDATE package_repositories SET url_template = ? WHERE id = ?",
			"http://packages.example/{version}.zip",
			f.source.ID,
		)
		require.NoError(t, err)
	}
	var requests atomic.Int64
	f.repo.ArtifactClient.HTTP.Transport = artifactRoundTrip(
		func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return nil, errors.New("unexpected fetch")
		})
	for _, call := range artifactPreparationCalls(f.repo) {
		if call.name != caller {
			continue
		}
		result, err := call.run(t.Context(), f.project.ID, version)
		require.Nil(t, result)
		if failure == "absent" && caller != "test" {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
			switch failure {
			case "absent", "source-missing":
				require.ErrorIs(t, err, sql.ErrNoRows)
			case "attachment-query", "source-query":
				require.ErrorContains(t, err, "unavailable_source")
			case "version", "url":
				require.ErrorIs(t, err, artifact.ErrInvalid)
			}
		}
	}
	require.Zero(t, requests.Load())
}

type artifactRoundTrip func(*http.Request) (*http.Response, error)

func (f artifactRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestArtifactPreparationPreservesMetadataAndDeferredCapture(t *testing.T) {
	f := newArtifactFixture(t)
	stored, err := f.repo.Queries.GetPackageRepository(t.Context(), f.source.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.Credential)
	require.NotEqual(t, "credential", stored.Credential)
	var requests atomic.Int64
	transport := f.repo.ArtifactClient.HTTP.Transport
	f.repo.ArtifactClient.HTTP.Transport = artifactRoundTrip(
		func(r *http.Request) (*http.Response, error) {
			requests.Add(1)
			return transport.RoundTrip(r)
		})
	f.repo.ArtifactClient.TempDir = t.TempDir()
	first, err := f.repo.CaptureProjectArtifact(
		t.Context(),
		f.project.ID,
		"1.2.3",
	)
	require.NoError(t, err)
	second, err := f.repo.CaptureProjectArtifact(
		t.Context(),
		f.project.ID,
		"1.2.3",
	)
	require.NoError(t, err)
	require.Zero(t, requests.Load())
	require.NotEmpty(t, first.Row.ResolutionKey)
	require.NotEmpty(t, second.Row.ResolutionKey)
	require.NotEqual(t, first.Row.ResolutionKey, second.Row.ResolutionKey)
	require.Empty(t, first.Row.Sha256)
	require.Zero(t, first.Row.Size)
	require.Equal(t, stored, first.Source)
	require.Equal(t, stored, second.Source)
	resolved, err := f.repo.ResolveProjectArtifact(
		t.Context(),
		f.project.ID,
		"1.2.3",
	)
	require.NoError(t, err)
	require.Equal(t, int64(1), requests.Load())
	require.Equal(t, stored, resolved.Source)
	require.Equal(t, first.Row.Url, resolved.Row.Url)
	require.Equal(t, stored.ID, resolved.Row.RepositoryID)
	require.NotEmpty(t, resolved.Row.Sha256)
	require.Positive(t, resolved.Row.Size)
	pin, err := f.repo.TestProjectPackage(t.Context(), f.project.ID, "1.2.3")
	require.NoError(t, err)
	require.Equal(t, int64(2), requests.Load())
	require.Equal(t, resolved.Row.Url, pin.URL)
	require.Equal(t, resolved.Row.Version, pin.Version)
	require.Equal(t, resolved.Row.Sha256, pin.SHA256)
	require.Equal(t, resolved.Row.Size, pin.Size)
	files, err := os.ReadDir(f.repo.ArtifactClient.TempDir)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestArtifactPreparationFetchErrors(t *testing.T) {
	for _, caller := range []string{"resolve", "test"} {
		for _, status := range []int{http.StatusNotFound, http.StatusBadGateway} {
			t.Run(caller+"/"+http.StatusText(status), func(t *testing.T) {
				testArtifactPreparationFetchError(t, caller, status)
			})
		}
	}
}

func testArtifactPreparationFetchError(
	t *testing.T,
	caller string,
	status int,
) {
	t.Helper()
	f := newArtifactFixture(t)
	f.repo.ArtifactClient.TempDir = t.TempDir()
	f.repo.ArtifactClient.HTTP.Transport = artifactRoundTrip(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status,
				Body: io.NopCloser(strings.NewReader(""))}, nil
		})
	for _, call := range artifactPreparationCalls(f.repo) {
		if call.name != caller {
			continue
		}
		_, err := call.run(t.Context(), f.project.ID, "1.2.3")
		var missing *repository.PackageMissingError
		if caller == "resolve" && status == http.StatusNotFound {
			require.ErrorAs(t, err, &missing)
			require.Equal(t, "1.2.3", missing.Version)
			require.Equal(t, f.source, missing.Source)
		} else {
			require.ErrorIs(t, err, artifact.ErrFetch)
			require.False(t, errors.As(err, &missing))
			if status == http.StatusNotFound {
				require.ErrorIs(t, err, artifact.ErrNotFound)
			}
		}
	}
	files, err := os.ReadDir(f.repo.ArtifactClient.TempDir)
	require.NoError(t, err)
	require.Empty(t, files)
}

type artifactCloseHook struct {
	io.ReadCloser
	afterClose func()
}

func (b artifactCloseHook) Close() error {
	err := b.ReadCloser.Close()
	b.afterClose()
	return err
}

func TestArtifactPreparationRemovalFailure(t *testing.T) {
	for _, caller := range []string{"resolve", "test"} {
		t.Run(caller, func(t *testing.T) {
			f := newArtifactFixture(t)
			directory := t.TempDir()
			f.repo.ArtifactClient.TempDir = directory
			transport := f.repo.ArtifactClient.HTTP.Transport
			f.repo.ArtifactClient.HTTP.Transport = artifactRoundTrip(
				func(r *http.Request) (*http.Response, error) {
					response, err := transport.RoundTrip(r)
					if err != nil {
						return response, err
					}
					response.Body = artifactCloseHook{response.Body, func() {
						// Fetch has closed its file. Replace it with a nonempty
						// directory so caller removal fails on every platform.
						files, err := filepath.Glob(
							filepath.Join(directory, "*.zip"),
						)
						require.NoError(t, err)
						require.Len(t, files, 1)
						path := files[0]
						require.NoError(t, os.Rename(path, path+".saved"))
						require.NoError(t, os.Mkdir(path, 0700))
						require.NoError(
							t,
							os.Rename(
								path+".saved",
								filepath.Join(path, "package"),
							),
						)
					}}
					return response, nil
				})
			for _, call := range artifactPreparationCalls(f.repo) {
				if call.name != caller {
					continue
				}
				result, err := call.run(t.Context(), f.project.ID, "1.2.3")
				require.Nil(t, result)
				var pathErr *os.PathError
				require.ErrorAs(t, err, &pathErr)
				require.Equal(t, "remove", pathErr.Op)
			}
		})
	}
}
