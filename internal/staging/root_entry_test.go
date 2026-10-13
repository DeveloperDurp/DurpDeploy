package staging

import (
	"archive/tar"
	"durpdeploy/internal/testutil"
	"errors"
	"os"
	"testing"
)

func TestSnapshotRootMustBeEmptyDirectory(t *testing.T) {
	for _, name := range []string{".", "./."} {
		for _, kind := range []byte{tar.TypeReg, tar.TypeDir} {
			t.Run(name+string(kind), func(t *testing.T) {
				file, err := os.CreateTemp(t.TempDir(), "archive")
				if err != nil {
					t.Fatal(err)
				}
				defer testutil.Close(t, file)
				writer := tar.NewWriter(file)
				if err := writer.WriteHeader(&tar.Header{
					Name: name, Typeflag: kind, Mode: 0700,
				}); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				snapshot, err := Read(t.Context(), file)
				if kind == tar.TypeDir {
					if err != nil || len(snapshot.Entries) != 0 {
						t.Fatalf("root directory: %+v %v", snapshot, err)
					}
				} else if !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted root file: %+v %v", snapshot, err)
				}
			})
		}
	}
}
