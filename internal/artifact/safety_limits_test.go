package artifact

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func safetyArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	for _, name := range names {
		entry, err := w.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(entry, strings.Repeat("x", 32))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return data.Bytes()
}

func TestConfiguredArtifactSafetyLimits(t *testing.T) {
	data := safetyArchive(t, "a/b/app.txt", "second.txt")
	for _, field := range []string{"bytes", "files", "nodes", "metadata"} {
		for _, accepted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", field, accepted), func(t *testing.T) {
				checkConfiguredArtifactSafetyLimit(t, data, field, accepted)
			})
		}
	}
}

func checkConfiguredArtifactSafetyLimit(
	t *testing.T,
	data []byte,
	field string,
	accepted bool,
) {
	t.Helper()
	limits := config.Defaults().Limits.Artifact
	switch field {
	case "bytes":
		limits.ExtractedBytes = 63
		if accepted {
			limits.ExtractedBytes++
		}
	case "files":
		limits.Files = 1
		if accepted {
			limits.Files++
		}
	case "nodes":
		limits.Files = 2
		limits.StagingNodes = 3
		if accepted {
			limits.StagingNodes++
		}
	case "metadata":
		limits.MetadataBytes = 1
		if accepted {
			limits.MetadataBytes = 8192
		}
	}
	server, trusted := artifactTLSServer(
		t,
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) },
		),
	)
	client := NewClientWithLimits(limits)
	transport := client.HTTP.Transport.(*http.Transport)
	transport.TLSClientConfig = trusted.HTTP.Transport.(*http.Transport).TLSClientConfig.Clone()
	t.Cleanup(transport.CloseIdleConnections)
	client.TempDir = t.TempDir()
	snapshot := limits
	limits = config.Defaults().Limits.Artifact // Mutating the caller must not relax the captured policy.
	download, err := client.Fetch(
		t.Context(),
		Repository{
			URLTemplate: server.URL + "/{version}.zip",
			AuthType:    "noauth",
		},
		Pin{URL: server.URL + "/1.zip"},
	)
	if accepted {
		require.NoError(t, err)
		directory, err := ExtractZIPWithLimits(
			t.Context(),
			download.Path,
			snapshot,
		)
		require.NoError(t, err)
		require.NoError(t, os.RemoveAll(directory))
		require.NoError(t, os.Remove(download.Path))
	} else {
		require.Error(t, err)
		filename := filepath.Join(client.TempDir, "rejected.zip")
		require.NoError(t, os.WriteFile(filename, data, 0600))
		_, err = ExtractZIPWithLimits(t.Context(), filename, snapshot)
		require.Error(t, err)
		require.NoError(t, os.Remove(filename))
	}
	entries, err := os.ReadDir(client.TempDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestRaisedArtifactFileAndNodeLimits(t *testing.T) {
	for _, nested := range []bool{false, true} {
		count := int(MaxFiles) + 1
		if nested {
			count = int(MaxFiles)
		}
		names := make([]string, count)
		for i := range names {
			names[i] = fmt.Sprintf("file-%d", i)
			if nested {
				names[i] += "/app.txt"
			}
		}
		filename := filepath.Join(t.TempDir(), "raised.zip")
		require.NoError(
			t,
			os.WriteFile(filename, safetyArchive(t, names...), 0600),
		)
		require.Error(t, ValidateZIP(t.Context(), filename))
		limits := config.Defaults().Limits.Artifact
		if nested {
			limits.StagingNodes++
		} else {
			limits.Files++
		}
		require.NoError(t, ValidateZIPWithLimits(t.Context(), filename, limits))
		directory, err := ExtractZIPWithLimits(t.Context(), filename, limits)
		require.NoError(t, err)
		require.NoError(t, os.RemoveAll(directory))
	}
}

func TestRaisedArtifactByteAndMetadataLimits(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		filename := filepath.Join(t.TempDir(), "raised.zip")
		file, err := os.Create(filename)
		require.NoError(t, err)
		w := zip.NewWriter(file)
		if metadata {
			for i := 0; i < 300; i++ {
				_, err = w.CreateHeader(
					&zip.FileHeader{
						Name:    fmt.Sprintf("file-%d", i),
						Comment: strings.Repeat("x", 65535),
					},
				)
				require.NoError(t, err)
			}
		} else {
			entry, err := w.Create("payload")
			require.NoError(t, err)
			_, err = io.CopyN(entry, zeroReader{}, MaxExtracted+1)
			require.NoError(t, err)
		}
		require.NoError(t, w.Close())
		require.NoError(t, file.Close())
		require.Error(t, ValidateZIP(t.Context(), filename))
		limits := config.Defaults().Limits.Artifact
		if metadata {
			limits.MetadataBytes = 64 << 20
		} else {
			limits.ExtractedBytes++
		}
		directory, err := ExtractZIPWithLimits(t.Context(), filename, limits)
		require.NoError(t, err)
		if !metadata {
			info, err := os.Stat(filepath.Join(directory, "payload"))
			require.NoError(t, err)
			require.Equal(t, MaxExtracted+1, info.Size())
		}
		require.NoError(t, os.RemoveAll(directory))
	}
}

func TestConfiguredApprovalBundleNodeLimit(t *testing.T) {
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	for name, text := range map[string]string{"plan": "plan", "review": `{"create":0}`, "extra": "extra"} {
		require.NoError(
			t,
			w.WriteHeader(
				&tar.Header{
					Name:     name,
					Typeflag: tar.TypeReg,
					Mode:     0600,
					Size:     int64(len(text)),
				},
			),
		)
		_, err := io.WriteString(w, text)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	for _, nodes := range []int64{2, 3} {
		limits := config.Defaults().Limits.Artifact
		limits.Files = 2
		limits.StagingNodes = nodes
		nodeErr := ValidateGateBundleNodes(
			t.Context(),
			bytes.NewReader(archive.Bytes()),
			nodes,
		)
		if nodes == 2 {
			require.ErrorIs(t, nodeErr, ErrInvalid)
		} else {
			require.NoError(t, nodeErr)
		}
		_, _, err := CopyGateBundleWithLimits(
			t.Context(),
			bytes.NewReader(archive.Bytes()),
			io.Discard,
			"plan",
			"review",
			"summary",
			limits,
		)
		if nodes == 2 {
			require.ErrorIs(t, err, ErrInvalid)
		} else {
			require.NoError(t, err)
		}
	}
}
